package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

type screenshotSettings struct {
	IntervalSec    float64 `json:"screenshot_interval_sec"`
	RetentionHours float64 `json:"screenshot_retention_hours"`
}
type screenshotSettingsStore struct {
	mu    sync.Mutex
	value screenshotSettings
	cfg   Config
}

func newScreenshotSettingsStore(cfg Config) *screenshotSettingsStore {
	return &screenshotSettingsStore{cfg: cfg, value: screenshotSettings{cfg.ScreenshotIntervalSec, cfg.ScreenshotRetentionHours}}
}
func (s *screenshotSettingsStore) get() screenshotSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value
}

var errRetentionConfirmation = errors.New("缩短保存时间会在后续清理中删除过期截图，请先确认")

func validateScreenshotSettings(v screenshotSettings) error {
	if math.IsNaN(v.IntervalSec) || math.IsInf(v.IntervalSec, 0) || v.IntervalSec < 10 || v.IntervalSec > 3600 || v.IntervalSec != math.Trunc(v.IntervalSec) {
		return fmt.Errorf("截图间隔须为 10～3600 的整数秒")
	}
	if math.IsNaN(v.RetentionHours) || math.IsInf(v.RetentionHours, 0) || v.RetentionHours < 1 || v.RetentionHours > 720 || v.RetentionHours != math.Trunc(v.RetentionHours) {
		return fmt.Errorf("截图保存时间须为 1～720 的整数小时")
	}
	return nil
}
func (s *screenshotSettingsStore) save(v screenshotSettings, confirm bool) error {
	if err := validateScreenshotSettings(v); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if v.RetentionHours < s.value.RetentionHours && !confirm {
		return errRetentionConfirmation
	}
	// 保留所有其他配置键；先成功落盘，才发布给采集线程。
	b, err := os.ReadFile(s.cfg.configPath)
	if os.IsNotExist(err) {
		b, err = json.Marshal(s.cfg)
	}
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(b, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("配置必须是 JSON 对象")
	}
	fields["screenshot_interval_sec"], _ = json.Marshal(v.IntervalSec)
	fields["screenshot_retention_hours"], _ = json.Marshal(v.RetentionHours)
	b, err = json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.cfg.configPath), ".settings-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), s.cfg.configPath); err != nil {
		return err
	}
	s.value = v
	return nil
}
func (d *dashboardServer) handleSettings(w http.ResponseWriter, r *http.Request) {
	if d.settings == nil {
		http.Error(w, "设置暂不可用", 503)
		return
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		writeDashboardJSON(w, d.settings.get())
		return
	}
	// 写接口要求同源和 JSON，拒绝网页表单及跨站请求。
	if r.Header.Get("Origin") != "http://"+dashboardAddr {
		http.Error(w, "设置修改必须来自本机仪表盘", 403)
		return
	}
	typ, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || typ != "application/json" {
		http.Error(w, "需要 JSON 请求", 415)
		return
	}
	var body struct {
		screenshotSettings
		ConfirmShorter bool `json:"confirm_shorter_retention"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&body); err != nil {
		http.Error(w, "设置格式错误", 400)
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "请求内容多余", 400)
		return
	}
	if err = validateScreenshotSettings(body.screenshotSettings); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err = d.settings.save(body.screenshotSettings, body.ConfirmShorter); err != nil {
		if errors.Is(err, errRetentionConfirmation) {
			http.Error(w, err.Error(), 409)
		} else {
			http.Error(w, "保存失败，未应用修改："+err.Error(), 500)
		}
		return
	}
	writeDashboardJSON(w, d.settings.get())
}
