package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func (a *App) wifiDir() string        { return filepath.Join(a.cfg.DataDir, "wifi") }
func (a *App) wifiEnablePath() string { return filepath.Join(a.wifiDir(), "enabled.flag") }

func (a *App) initWiFiIfEnabled() {
	enabled := fileExists(a.wifiEnablePath())
	a.wifiEnabled.Store(enabled)
	if !enabled {
		a.wifiLastStatus.Store("disabled")
		return
	}
	if a.wifi != nil {
		return
	}
	m, err := openWiFiManager()
	if err != nil {
		a.wifiFailures.Add(1)
		a.wifiLastStatus.Store(classifyWiFiError(err))
		a.store.Log("Wi-Fi 初始化失败: " + err.Error())
		return
	}
	a.wifi = m
	a.wifiLastStatus.Store("ready")
	a.store.Log("Wi-Fi 位置指纹已启用")
}

func (a *App) maybeMaintainWiFi() {
	now := time.Now()
	cleanupDue := a.lastWiFiCleanupMono.IsZero() || now.Sub(a.lastWiFiCleanupMono) >= 10*time.Minute
	if cleanupDue && a.wifiBusy.CompareAndSwap(false, true) {
		a.lastWiFiCleanupMono = now
		a.wifiWG.Add(1)
		go func() {
			defer a.wifiWG.Done()
			defer a.wifiBusy.Store(false)
			removed, err := cleanupWiFiFiles(a.wifiDir(), time.Now(), a.cfg.WiFiRetentionDays)
			if err != nil {
				a.wifiFailures.Add(1)
				a.store.Log("清理 Wi-Fi 历史失败: " + err.Error())
			}
			if removed > 0 {
				a.store.Log(fmt.Sprintf("已删除 %d 个超过 %d 天的 Wi-Fi 数据文件", removed, a.cfg.WiFiRetentionDays))
			}
		}()
	}
	if !a.wifiEnabled.Load() || a.wifiPaused.Load() || a.suspended {
		return
	}
	if a.wifi == nil {
		a.initWiFiIfEnabled()
		if a.wifi == nil {
			return
		}
	}
	if a.wifiPendingScan {
		if time.Since(a.wifiScanStartedMono) >= 6*time.Second {
			a.wifiPendingScan = false
			a.lastWiFiSnapshotMono = now
			a.writeWiFiError("scan_timeout", "active_scan", errors.New("WlanScan 6 秒内未收到完成通知"))
		}
		return
	}
	interval := time.Duration(a.cfg.WiFiSnapshotIntervalSec * float64(time.Second))
	if !a.lastWiFiSnapshotMono.IsZero() && now.Sub(a.lastWiFiSnapshotMono) < interval {
		return
	}
	activeEvery := time.Duration(a.cfg.WiFiActiveScanIntervalSec * float64(time.Second))
	if a.lastWiFiScanMono.IsZero() || now.Sub(a.lastWiFiScanMono) >= activeEvery {
		a.requestWiFiActiveScan("periodic")
		return
	}
	a.collectWiFi("periodic", "cache")
}

func (a *App) requestWiFiActiveScan(reason string) {
	if !a.wifiEnabled.Load() || a.wifiPaused.Load() || a.suspended || a.wifiPendingScan {
		return
	}
	if a.wifi == nil {
		a.initWiFiIfEnabled()
		if a.wifi == nil {
			return
		}
	}
	if err := a.wifi.requestScan(); err != nil {
		a.lastWiFiSnapshotMono = time.Now()
		a.writeWiFiError(reason, "active_scan", err)
		return
	}
	a.lastWiFiScanMono = time.Now()
	a.wifiScanStartedMono = time.Now()
	a.wifiPendingScan = true
	a.wifiLastStatus.Store("scanning")
}

func (a *App) onWiFiEvent(code uint32) {
	if !a.wifiEnabled.Load() || a.wifiPaused.Load() || a.suspended {
		return
	}
	switch code {
	case wlanNotificationScanComplete:
		if a.wifiPendingScan {
			a.wifiPendingScan = false
			a.collectWiFi("active_scan", "active_scan")
		}
	case wlanNotificationScanFail:
		if a.wifiPendingScan {
			a.wifiPendingScan = false
			a.lastWiFiSnapshotMono = time.Now()
			a.writeWiFiError("scan_failed", "active_scan", errors.New("Windows 报告 Wi-Fi 扫描失败"))
		}
	case wlanNotificationConnectionComplete, wlanNotificationDisconnected,
		wlanNotificationInterfaceArrival, wlanNotificationInterfaceRemoval:
		a.lastWiFiScanMono = time.Time{}
		a.requestWiFiActiveScan("connection_change")
	}
}

func (a *App) collectWiFi(event, source string) {
	if !a.wifiBusy.CompareAndSwap(false, true) {
		return
	}
	a.lastWiFiSnapshotMono = time.Now()
	a.wifiWG.Add(1)
	go func() {
		defer a.wifiWG.Done()
		defer a.wifiBusy.Store(false)
		ifs, err := a.wifi.snapshot()
		if !a.wifiEnabled.Load() || a.wifiPaused.Load() {
			return
		}
		status := classifyWiFiError(err)
		rec := newWiFiRecord(event, source, status, ifs, err)
		if werr := appendWiFiRecord(a.wifiDir(), rec); werr != nil {
			a.wifiFailures.Add(1)
			a.wifiLastStatus.Store("write_error")
			a.store.Log("写入 Wi-Fi 数据失败: " + werr.Error())
			return
		}
		a.wifiWritten.Add(1)
		a.wifiLastTS.Store(rec.TS)
		a.wifiLastStatus.Store(status)
		if err != nil {
			a.wifiFailures.Add(1)
			a.store.Log("Wi-Fi 采集失败: " + err.Error())
			if status == "access_denied" && a.wifiPermissionNotice.CompareAndSwap(false, true) {
				a.notify("Wi-Fi 位置权限未开启", "请在托盘菜单打开 Windows 位置权限设置，然后允许桌面应用访问位置")
			}
		}
	}()
}

func (a *App) writeWiFiError(event, source string, err error) {
	if !a.wifiEnabled.Load() || a.wifiPaused.Load() {
		return
	}
	status := classifyWiFiError(err)
	if event == "scan_timeout" {
		status = "scan_timeout"
	}
	if event == "scan_failed" {
		status = "scan_failed"
	}
	rec := newWiFiRecord(event, source, status, nil, err)
	if werr := appendWiFiRecord(a.wifiDir(), rec); werr != nil {
		a.wifiFailures.Add(1)
		a.wifiLastStatus.Store("write_error")
		a.store.Log("写入 Wi-Fi 错误记录失败: " + werr.Error())
		return
	}
	a.wifiWritten.Add(1)
	a.wifiFailures.Add(1)
	a.wifiLastTS.Store(rec.TS)
	a.wifiLastStatus.Store(status)
	a.store.Log("Wi-Fi " + event + ": " + err.Error())
}

func (a *App) writeWiFiMarker(event, status string) {
	if !a.wifiEnabled.Load() {
		return
	}
	rec := newWiFiRecord(event, "control", status, nil, nil)
	if err := appendWiFiRecord(a.wifiDir(), rec); err != nil {
		a.wifiFailures.Add(1)
		a.wifiLastStatus.Store("write_error")
		a.store.Log("写入 Wi-Fi 控制标志失败: " + err.Error())
		return
	}
	a.wifiWritten.Add(1)
	a.wifiLastTS.Store(rec.TS)
	a.wifiLastStatus.Store(status)
}

func (a *App) toggleWiFi() {
	if a.wifiEnabled.Load() {
		if err := os.Remove(a.wifiEnablePath()); err != nil && !os.IsNotExist(err) {
			a.store.Log("停用 Wi-Fi 失败: " + err.Error())
			return
		}
		a.writeWiFiMarker("disabled", "disabled")
		a.wifiEnabled.Store(false)
		a.wifiPendingScan = false
		a.wifiLastStatus.Store("disabled")
		a.notify("Wi-Fi 位置指纹已停用", "不会再保存附近 Wi-Fi 细节；已有数据仍按 15 天滚动清理")
		return
	}
	if err := os.MkdirAll(a.wifiDir(), 0o755); err != nil {
		a.store.Log("创建 Wi-Fi 数据目录失败: " + err.Error())
		return
	}
	content := time.Now().Format("2006-01-02T15:04:05-07:00") + newline
	if err := os.WriteFile(a.wifiEnablePath(), []byte(content), 0o644); err != nil {
		a.store.Log("启用 Wi-Fi 失败: " + err.Error())
		return
	}
	a.wifiEnabled.Store(true)
	a.wifiPermissionNotice.Store(false)
	a.initWiFiIfEnabled()
	status := "ready"
	if v := a.wifiLastStatus.Load(); v != nil {
		status = v.(string)
	}
	a.writeWiFiMarker("enabled", status)
	a.notify("正在启用 Wi-Fi 位置指纹", "Windows 如询问位置权限，请选择允许；之后将自动运行")
	a.lastWiFiScanMono = time.Time{}
	a.lastWiFiSnapshotMono = time.Time{}
	a.requestWiFiActiveScan("user_enable")
}

func (a *App) openLocationSettings() {
	cmd := exec.Command("explorer.exe", "ms-settings:privacy-location")
	if err := cmd.Start(); err != nil {
		a.store.Log("打开位置权限设置失败: " + err.Error())
		return
	}
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
}

func (a *App) wifiStateText() string {
	if !a.wifiEnabled.Load() {
		return "未启用"
	}
	if a.paused {
		return "随采集暂停"
	}
	if v := a.wifiLastStatus.Load(); v != nil {
		switch v.(string) {
		case "ok":
			return "正常"
		case "ready":
			return "已启用，等待首条数据"
		case "scanning":
			return "扫描中"
		case "access_denied":
			return "需要位置权限"
		case "radio_off":
			return "无线电已关闭"
		case "no_interface":
			return "无 Wi-Fi 适配器"
		case "service_inactive":
			return "WLAN 服务未运行"
		case "scan_timeout":
			return "最近扫描超时"
		case "scan_failed":
			return "最近扫描失败"
		case "write_error":
			return "写盘失败"
		}
	}
	return "已启用"
}
