package main

import (
	"encoding/json"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

const collectorName = "屏幕时间采集器"
const mutexName = "ScreenTimeObserver.M1.Collector"

type Config struct {
	configPath                string
	DataDir                   string  `json:"data_dir"`
	LogDir                    string  `json:"log_dir"`
	IdleThresholdSec          float64 `json:"idle_threshold_sec"`
	TickSec                   float64 `json:"tick_sec"`
	ReconcileSec              float64 `json:"reconcile_sec"`
	HeartbeatSec              float64 `json:"heartbeat_sec"`
	PausedHeartbeatSec        float64 `json:"paused_heartbeat_sec"`
	GapReportSec              float64 `json:"gap_report_sec"`
	TitleMaxLen               int     `json:"title_max_len"`
	Tray                      bool    `json:"tray"`
	IconPath                  string  `json:"icon_path"`
	IconMonoPath              string  `json:"icon_mono_path"`
	IconStateDir              string  `json:"icon_state_dir"`
	ScreenshotEnabled         bool    `json:"screenshot_enabled"`
	ScreenshotIntervalSec     float64 `json:"screenshot_interval_sec"`
	ScreenshotRetentionHours  float64 `json:"screenshot_retention_hours"`
	WiFiSnapshotIntervalSec   float64 `json:"wifi_snapshot_interval_sec"`
	WiFiActiveScanIntervalSec float64 `json:"wifi_active_scan_interval_sec"`
	WiFiRetentionDays         int     `json:"wifi_retention_days"`
}

func defaultConfig(exeDir string) Config {
	return Config{
		configPath:                filepath.Join(exeDir, "config.json"),
		DataDir:                   filepath.Join(exeDir, "data"),
		LogDir:                    filepath.Join(exeDir, "logs"),
		IdleThresholdSec:          180,
		TickSec:                   1,
		ReconcileSec:              30,
		HeartbeatSec:              60,
		PausedHeartbeatSec:        60,
		GapReportSec:              90,
		TitleMaxLen:               300,
		Tray:                      true,
		IconPath:                  filepath.Join(exeDir, "assets", "logo.ico"),
		IconMonoPath:              filepath.Join(exeDir, "assets", "logo-mono.ico"),
		IconStateDir:              filepath.Join(exeDir, "assets", "states"),
		ScreenshotEnabled:         true,
		ScreenshotIntervalSec:     60,
		ScreenshotRetentionHours:  24,
		WiFiSnapshotIntervalSec:   300,
		WiFiActiveScanIntervalSec: 1800,
		WiFiRetentionDays:         15,
	}
}

// loadConfig 以默认值为底，用 config.json 覆盖（缺项保持默认）。
func loadConfig(exeDir, path string) (Config, error) {
	cfg := defaultConfig(exeDir)
	cfg.configPath = path
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, err
	}
	// 路径归一化：无论配置里写成 / 还是 \，下游统一拿到系统原生分隔符
	for _, p := range []*string{&cfg.DataDir, &cfg.LogDir, &cfg.IconPath, &cfg.IconMonoPath, &cfg.IconStateDir} {
		*p = filepath.FromSlash(*p)
		if abs, err := filepath.Abs(*p); err == nil {
			*p = abs
		}
	}
	if cfg.ScreenshotIntervalSec <= 0 {
		cfg.ScreenshotIntervalSec = 60
	}
	if cfg.ScreenshotRetentionHours <= 0 {
		cfg.ScreenshotRetentionHours = 24
	}
	if cfg.WiFiSnapshotIntervalSec <= 0 {
		cfg.WiFiSnapshotIntervalSec = 300
	}
	if cfg.WiFiActiveScanIntervalSec <= 0 {
		cfg.WiFiActiveScanIntervalSec = 1800
	}
	if cfg.WiFiRetentionDays <= 0 {
		cfg.WiFiRetentionDays = 15
	}
	return cfg, nil
}

// App 持有全部运行状态。主线程跑消息循环，写盘在 Store 的独立 goroutine 里。
type App struct {
	settings  *screenshotSettingsStore
	dashboard *dashboardServer
	cfg       Config
	exeDir    string
	store     *Store
	pausePath string

	startedMono time.Time
	startedAt   time.Time

	fg          Sample
	idleSec     float64
	idle        bool
	media       bool
	audio       bool
	audioPeak   float32
	meter       *audioMeter
	audioLogged bool
	locked      bool
	suspended   bool
	paused      bool

	lastKey                   string
	lastEpoch                 float64
	lastWriteMono             time.Time
	lastReconcileMono         time.Time
	lastPausedWrite           time.Time
	lastStatusMono            time.Time
	lastScreenshotMono        time.Time
	lastScreenshotCleanupMono time.Time
	lastWiFiSnapshotMono      time.Time
	lastWiFiScanMono          time.Time
	lastWiFiCleanupMono       time.Time
	wifiScanStartedMono       time.Time
	carryDay                  string

	tickCount int64
	fgEvents  int64

	dropsSeen    int64
	lastDropMono time.Time

	screenshotWG          sync.WaitGroup
	screenshotBusy        atomic.Bool
	screenshotPaused      atomic.Bool
	screenshotsWritten    atomic.Int64
	screenshotFailures    atomic.Int64
	lastScreenshotTS      atomic.Value
	lastScreenshotPath    atomic.Value
	lastScreenshotFailure atomic.Int64

	wifi                 *wifiManager
	wifiWG               sync.WaitGroup
	wifiBusy             atomic.Bool
	wifiEnabled          atomic.Bool
	wifiPaused           atomic.Bool
	wifiPendingScan      bool
	wifiWritten          atomic.Int64
	wifiFailures         atomic.Int64
	wifiLastTS           atomic.Value
	wifiLastStatus       atomic.Value
	wifiPermissionNotice atomic.Bool

	hwnd          uintptr
	trayIconH     uintptr
	trayIconOwned bool
	trayIconLight bool
	trayIconState string
	taskbarMsg    uint32
	trayAttempts  int
	lastTrayTry   time.Time
	nid           *notifyIconData
	trayOK        bool
	hookFG        uintptr
	hookMin       uintptr
	hookOK        bool
	headless      bool
	wndProcCB     uintptr
	winEventCB    uintptr

	quitFlag   bool
	exitReason string
	verbose    bool
}

func newApp(cfg Config, exeDir string, headless, verbose bool) *App {
	return &App{
		settings:  newScreenshotSettingsStore(cfg),
		cfg:       cfg,
		exeDir:    exeDir,
		pausePath: filepath.Join(exeDir, "paused.flag"),
		headless:  headless,
		verbose:   verbose,
		meter:     &audioMeter{},
	}
}

// ---------------- 记录构造 ----------------

func clip(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

func (a *App) session() string {
	switch {
	case a.suspended:
		return "suspended"
	case a.locked:
		return "locked"
	default:
		return "active"
	}
}

func (a *App) newRecord(event, reason string) Record {
	now := time.Now()
	rec := Record{
		TS:     now.Format("2006-01-02T15:04:05.000-07:00"),
		Epoch:  math.Round(float64(now.UnixNano())/1e6) / 1000,
		Event:  event,
		Reason: reason,
	}
	if a.lastEpoch > 0 {
		d := math.Round((rec.Epoch-a.lastEpoch)*1000) / 1000
		rec.SincePrev = &d
		rec.Gap = d > a.cfg.GapReportSec
	}
	return rec
}

func (a *App) stateRecord(event, reason string) Record {
	rec := a.newRecord(event, reason)
	if a.paused {
		rec.Session = "paused"
		rec.Paused = true
		return rec
	}
	rec.Session = a.session()
	rec.Process = a.fg.Process
	rec.PID = a.fg.PID
	rec.Title = clip(a.fg.Title, a.cfg.TitleMaxLen)
	rec.WindowClass = a.fg.WindowClass
	rec.IdleSec = nanSeconds(a.idleSec)
	rec.Idle = a.idle
	rec.Media = a.media
	rec.Locked = a.locked
	rec.Paused = false
	return rec
}

// pauseRecord 只有暂停标志，不含任何窗口细节。
func (a *App) pauseRecord(event string) Record {
	rec := a.newRecord(event, event)
	rec.Session = "paused"
	rec.Paused = true
	return rec
}

func (a *App) emit(rec Record) {
	a.lastEpoch = rec.Epoch
	a.store.Enqueue(rec)
	if a.verbose {
		if b, err := json.Marshal(rec); err == nil {
			os.Stdout.Write(append(b, newline...))
		}
	}
}

func (a *App) emitState(event, reason string) {
	a.emit(a.stateRecord(event, reason))
	a.lastKey = a.key()
	a.lastWriteMono = time.Now()
}

func (a *App) key() string {
	return a.session() + "|" + itoa(boolToInt(a.idle)) + "|" + itoa(boolToInt(a.media)) + "|" + a.fg.Process + "|" +
		itoa(int64(a.fg.PID)) + "|" + a.fg.Title + "|" + itoa(boolToInt(a.paused))
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// ---------------- 采样驱动 ----------------

// refreshIdle 采集键鼠空闲秒数，并区分「真的离开了」与「在看/听东西」。
// 没人动键鼠但有声音在放，说明屏幕上有内容，那是 media，不是 idle。
func (a *App) refreshIdle() bool {
	if a.paused {
		a.idleSec = math.NaN()
		a.idle = false
		a.media = false
		return false
	}
	was := a.idleKey()
	a.idleSec = idleSeconds()
	inputIdle := !math.IsNaN(a.idleSec) && a.idleSec >= a.cfg.IdleThresholdSec
	a.audio = false
	if inputIdle {
		a.audio = a.audioPlaying()
	} else {
		a.audioPeak = 0
	}
	a.idle, a.media = idleState(a.idleSec, a.cfg.IdleThresholdSec, a.audio)
	return was != a.idleKey()
}

// idleState 判定一段时间没键鼠输入时到底算什么：
// 没到阈值就是正常使用；到了阈值但有声音在放，说明人在看/听，记成 media；两者都不是才叫空闲。
func idleState(idleSec, threshold float64, audioPlaying bool) (idle bool, media bool) {
	if math.IsNaN(idleSec) || idleSec < threshold {
		return false, false
	}
	if audioPlaying {
		return false, true
	}
	return true, false
}

func (a *App) idleKey() string {
	return itoa(boolToInt(a.idle)) + itoa(boolToInt(a.media))
}

// audioPlaying 问系统默认输出设备是否正在出声。只有疑似空闲时才会调用，
// 所以正常使用电脑时完全不碰音频接口。查询失败按「没在放」处理，只记一次日志。
func (a *App) audioPlaying() bool {
	if a.meter == nil {
		return false
	}
	peak, err := a.meter.peak()
	if err != nil {
		if !a.audioLogged {
			a.audioLogged = true
			a.store.Log("音频检测不可用，空闲判定回退到纯键鼠: " + err.Error())
		}
		return false
	}
	a.audioPeak = peak
	return peak > audioPeakThreshold
}

func (a *App) refreshForeground() {
	if a.paused {
		a.fg = Sample{}
		return
	}
	a.fg = sampleForeground()
	a.locked = sessionLocked()
}

func (a *App) syncPause() {
	want := fileExists(a.pausePath)
	if want == a.paused {
		return
	}
	a.paused = want
	a.screenshotPaused.Store(want)
	a.wifiPaused.Store(want)
	if a.paused {
		a.writeWiFiMarker("paused", "paused")
		a.fg = Sample{}
		a.idleSec = math.NaN()
		a.idle = false
		a.emit(a.pauseRecord("paused"))
		a.lastPausedWrite = time.Now()
		a.store.Log("paused by user (flag file present)")
	} else {
		a.lastScreenshotMono = time.Time{}
		a.lastWiFiSnapshotMono = time.Time{}
		a.refreshForeground()
		a.refreshIdle()
		a.emitState("resumed", "user_resume")
		a.store.Log("resumed by user")
		a.writeWiFiMarker("resumed", "ready")
		a.requestWiFiActiveScan("user_resume")
	}
	a.writeStatus()
	a.refreshTrayTip()
	a.refreshTrayIcon()
}

func (a *App) tick() {
	a.tickCount++
	a.syncPause()
	a.maybeMaintainScreenshots()
	a.maybeMaintainWiFi()

	if a.suspended {
		return
	}
	a.maybeCarryOver()

	if a.paused {
		a.idleSec = math.NaN()
		a.media = false
		if time.Since(a.lastPausedWrite).Seconds() >= a.cfg.PausedHeartbeatSec {
			a.emit(a.pauseRecord("paused"))
			a.lastPausedWrite = time.Now()
		}
		return
	}

	idleChanged := a.refreshIdle()
	if idleChanged {
		a.emitState("change", "idle_threshold")
		return
	}
	if time.Since(a.lastWriteMono).Seconds() >= a.cfg.HeartbeatSec {
		a.emitState("heartbeat", "heartbeat")
	}
}

// maybeCarryOver 在新的一天写入承接记录，明确延续上一天最后的状态。
func (a *App) maybeCarryOver() {
	day := time.Now().Format("2006-01-02")
	if a.carryDay == day {
		return
	}
	a.carryDay = day
	if a.lastEpoch == 0 {
		return
	}
	a.emitState("carry_over", "new_day")
}

// onForeground 前台窗口变化（事件驱动主通道）。
func (a *App) onForeground() {
	if a.suspended || a.paused {
		return
	}
	a.fgEvents++
	a.refreshIdle()
	a.refreshForeground()
	if a.key() == a.lastKey {
		// 同一次切换可能成对到达 FOREGROUND 与 MINIMIZEEND 两个事件，状态没变就不写重复记录
		return
	}
	a.emitState("change", "foreground")
}

// reconcile 低频对账：兜住漏掉的事件，并刷新状态文件与托盘提示。
func (a *App) reconcile() {
	a.lastReconcileMono = time.Now()
	if !a.suspended && !a.paused {
		a.refreshForeground()
		a.refreshIdle()
		if a.key() != a.lastKey {
			a.emitState("change", "reconcile")
			a.store.Log("reconcile fixed a missed state change")
		}
	}
	a.writeStatus()
	a.refreshTrayTip()
}

func (a *App) writeStatus() {
	idle := a.cfg.IdleThresholdSec
	st := map[string]any{
		"process":              "collector",
		"pid":                  os.Getpid(),
		"updated":              time.Now().Format("2006-01-02T15:04:05-07:00"),
		"started_at":           a.startedAt.Format("2006-01-02T15:04:05-07:00"),
		"uptime_sec":           int(time.Since(a.startedMono).Seconds()),
		"ticks":                a.tickCount,
		"foreground_events":    a.fgEvents,
		"records_written":      a.store.Written(),
		"records_dropped":      a.store.Dropped(),
		"paused":               a.paused,
		"session":              a.session(),
		"idle":                 a.idle,
		"media":                a.media,
		"audio_peak":           math.Round(float64(a.audioPeak)*10000) / 10000,
		"idle_sec":             nanSeconds(a.idleSec),
		"idle_threshold_sec":   idle,
		"foreground_process":   a.fg.Process,
		"foreground_title":     clip(a.fg.Title, 120),
		"hook_ok":              a.hookOK,
		"tray_ok":              a.trayOK,
		"cpu_sec":              math.Round(processCPUSeconds()*100) / 100,
		"data_dir":             a.cfg.DataDir,
		"screenshot_enabled":   a.cfg.ScreenshotEnabled,
		"screenshot_dir":       a.screenshotDir(),
		"screenshots_written":  a.screenshotsWritten.Load(),
		"screenshot_failures":  a.screenshotFailures.Load(),
		"screenshot_busy":      a.screenshotBusy.Load(),
		"wifi_enabled":         a.wifiEnabled.Load(),
		"wifi_dir":             a.wifiDir(),
		"wifi_busy":            a.wifiBusy.Load(),
		"wifi_records_written": a.wifiWritten.Load(),
		"wifi_failures":        a.wifiFailures.Load(),
	}
	if v := a.lastScreenshotTS.Load(); v != nil {
		st["last_screenshot_ts"] = v.(string)
	}
	if v := a.lastScreenshotPath.Load(); v != nil {
		st["last_screenshot_path"] = v.(string)
	}
	if v := a.wifiLastTS.Load(); v != nil {
		st["last_wifi_ts"] = v.(string)
	}
	if v := a.wifiLastStatus.Load(); v != nil {
		st["wifi_status"] = v.(string)
	}
	for k, v := range memStats() {
		st[k] = v
	}
	for k, v := range resourceCounts() {
		st[k] = v
	}
	a.store.WriteStatus(st)
	a.lastStatusMono = time.Now()
}

// ---------------- 电源与会话 ----------------

func (a *App) onSuspend() {
	if a.suspended {
		return
	}
	a.suspended = true
	a.emitState("offline", "system_suspend")
	a.store.Log("system suspend")
	a.refreshTrayTip()
}

func (a *App) onResume() {
	if !a.suspended {
		return
	}
	a.suspended = false
	a.refreshForeground()
	a.refreshIdle()
	a.emitState("online", "system_resume")
	a.store.Log("system resume")
	a.lastWiFiSnapshotMono = time.Time{}
	a.requestWiFiActiveScan("system_resume")
	a.refreshTrayTip()
}

// ---------------- 生命周期 ----------------

func (a *App) shutdown(reason string) {
	if a.quitFlag {
		return
	}
	a.quitFlag = true
	a.exitReason = reason
	if !a.paused {
		a.refreshForeground()
	}
	rec := a.stateRecord("stop", reason)
	rec.UptimeSec = floatPtr(math.Round(time.Since(a.startedMono).Seconds()))
	if a.paused {
		rec.Process = ""
		rec.Title = ""
		rec.WindowClass = ""
		rec.IdleSec = nil
		rec.Paused = true
	}
	a.emit(rec)
	a.writeStatus()
	a.store.Log("collector stop reason=" + reason + " records=" + itoa(a.store.Written()) +
		" dropped=" + itoa(a.store.Dropped()))
	a.removeTray()
	if a.dashboard != nil {
		_ = a.dashboard.server.Close()
	}
	a.screenshotWG.Wait()
	a.wifiWG.Wait()
	if a.wifi != nil {
		a.wifi.close()
	}
	a.store.Close()
	procPostQuitMessage.Call(0)
}

func (a *App) openDataDir() {
	// explorer.exe 只认反斜杠；手改配置可能写成正斜杠，这里统一归一化并转绝对路径。
	dir, err := filepath.Abs(filepath.FromSlash(a.cfg.DataDir))
	if err != nil {
		a.store.Log("cannot resolve data dir: " + err.Error())
		return
	}
	if !fileExists(dir) {
		a.store.Log("data dir does not exist: " + dir)
		return
	}
	a.store.Log("opening data dir: " + dir)
	cmd := exec.Command("explorer.exe", dir)
	if err := cmd.Start(); err != nil {
		a.store.Log("cannot open data dir: " + err.Error())
		return
	}
	// explorer.exe 立刻交棒给已有实例后自己退出；不等它，但必须释放进程句柄，否则每次点击漏一个句柄
	if cmd.Process != nil {
		cmd.Process.Release()
	}
}

func (a *App) togglePause() {
	defer a.syncPause()
	if fileExists(a.pausePath) {
		if err := os.Remove(a.pausePath); err != nil {
			a.store.Log("cannot clear pause flag: " + err.Error())
			return
		}
	} else {
		content := time.Now().Format("2006-01-02T15:04:05-07:00") + newline
		if err := os.WriteFile(a.pausePath, []byte(content), 0o644); err != nil {
			a.store.Log("cannot set pause flag: " + err.Error())
		}
	}
}

func (a *App) stateText() string {
	switch {
	case a.alerting():
		return "采集异常"
	case a.paused:
		return "已暂停（不记录细节）"
	case a.suspended:
		return "系统休眠中"
	case a.locked:
		return "已锁屏"
	case a.idle:
		return "空闲中"
	default:
		return "记录中"
	}
}

// stateKey 是托盘图标的状态分类，优先级由高到低：异常 > 暂停 > 空闲锁屏休眠 > 记录中。
func (a *App) stateKey() string {
	switch {
	case a.alerting():
		return "alert"
	case a.paused:
		return "paused"
	case a.suspended || a.locked || a.idle:
		return "idle"
	default:
		return "active"
	}
}

// alerting 表示现在可能采不到数据：事件钩子失效，或最近 10 分钟有过写盘失败。
// 刻意不看托盘是否注册成功：托盘自身没起来时根本没有图标可着色，
// 而且加载图标发生在注册之前，看它会在启动瞬间误报一次异常。
func (a *App) alerting() bool {
	if !a.hookOK {
		return true
	}
	if n := a.lastScreenshotFailure.Load(); n > 0 && time.Since(time.Unix(0, n)) < 10*time.Minute {
		return true
	}
	return !a.lastDropMono.IsZero() && time.Since(a.lastDropMono) < 10*time.Minute
}

// alertDetail 说明异常卡在哪一环，供气泡与提示文字使用。
func (a *App) alertDetail() string {
	switch {
	case !a.hookOK:
		return "前台事件钩子失效，窗口切换可能漏记"
	case a.lastScreenshotFailure.Load() > 0 && time.Since(time.Unix(0, a.lastScreenshotFailure.Load())) < 10*time.Minute:
		return "最近有桌面截图失败，请查看日志"
	case a.lastDropMono.IsZero():
		return "状态未知，请查看日志"
	default:
		return "最近有记录写盘失败，请检查数据目录"
	}
}

// pollDrops 记录最近一次写盘失败的时间，让异常状态能自己恢复，而不是一直停红。
func (a *App) pollDrops() {
	if a.store == nil {
		return
	}
	if n := a.store.Dropped(); n > a.dropsSeen {
		a.dropsSeen = n
		a.lastDropMono = time.Now()
		a.store.Log("write failures detected: total=" + itoa(n))
	}
}

// lastRecordEpoch 读当天数据文件的最后一条记录时间，用来给「进程重启造成的空洞」打标。
// 只读文件尾部一小段，不整文件加载。
// lastRecordEpoch 读最近一份数据文件的最后一条记录时间，用来给「进程重启造成的空洞」打标。
// 跨日重启时也要能找到上一次的记录，所以取日期最大的那份文件。
// 只读文件尾部一小段，不整文件加载。
func lastRecordEpoch(dataDir string) float64 {
	days := dayFiles(dataDir)
	if len(days) == 0 {
		return 0
	}
	path := filepath.Join(dataDir, days[len(days)-1]+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.Size() == 0 {
		return 0
	}
	const tail = 8192
	start := int64(0)
	if fi.Size() > tail {
		start = fi.Size() - tail
	}
	buf := make([]byte, fi.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return 0
	}
	lines := strings.Split(string(buf), newline)
	for i := len(lines) - 1; i >= 0; i-- {
		s := strings.TrimSpace(lines[i])
		if s == "" {
			continue
		}
		var r Record
		if json.Unmarshal([]byte(s), &r) == nil && r.Epoch > 0 {
			return r.Epoch
		}
	}
	return 0
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// memStats 暴露 Go 运行时的真实内存数字，便于长期盯有无泄漏。
// resourceCounts 取本进程的内核句柄数与 GDI/USER 对象数，用于长期盯托盘相关泄漏。
func resourceCounts() map[string]any {
	out := map[string]any{}
	cur, _, _ := procGetCurrentProcess.Call()
	var handles uint32
	if ok, _, _ := procGetProcessHandleCount.Call(cur, uintptr(unsafe.Pointer(&handles))); ok != 0 {
		out["handles"] = handles
	}
	if v, _, _ := procGetGuiResources.Call(cur, grGdiObjects); v != 0 {
		out["gdi_objects"] = uint32(v)
	}
	if v, _, _ := procGetGuiResources.Call(cur, grUserObjects); v != 0 {
		out["user_objects"] = uint32(v)
	}
	return out
}

func memStats() map[string]any {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	const mb = 1048576.0
	return map[string]any{
		"go_heap_alloc_mb": round2(float64(m.HeapAlloc) / mb),
		"go_heap_sys_mb":   round2(float64(m.HeapSys) / mb),
		"go_stack_mb":      round2(float64(m.StackInuse) / mb),
		"go_total_sys_mb":  round2(float64(m.Sys) / mb),
		"go_num_gc":        m.NumGC,
		"go_heap_objects":  m.HeapObjects,
	}
}

func singleInstance() (uintptr, error) {
	name := u16Ptr(mutexName)
	h, _, err := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return 0, err
	}
	// ERROR_ALREADY_EXISTS 时 CreateMutex 仍返回句柄，需要读 last error
	if e, ok := err.(syscall.Errno); ok && uintptr(e) == errorAlreadyExists {
		return 0, err
	}
	return h, nil
}
