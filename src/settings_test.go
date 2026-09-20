package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScreenshotSettingsPersistence(t *testing.T) {
	dir := t.TempDir()
	cfg := defaultConfig(dir)
	original := `{"data_dir":"D:/keep-data","custom_key":"preserved","screenshot_interval_sec":60,"screenshot_retention_hours":24}`
	if err := os.WriteFile(cfg.configPath, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	s := newScreenshotSettingsStore(cfg)
	if err := s.save(screenshotSettings{30, 12}, false); err != errRetentionConfirmation {
		t.Fatalf("未确认删除应拒绝: %v", err)
	}
	if err := s.save(screenshotSettings{30, 12}, true); err != nil {
		t.Fatal(err)
	}
	reloaded, err := loadConfig(dir, cfg.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ScreenshotIntervalSec != 30 || reloaded.ScreenshotRetentionHours != 12 {
		t.Fatal("重启后配置未持久化")
	}
	b, _ := os.ReadFile(cfg.configPath)
	var v map[string]any
	json.Unmarshal(b, &v)
	if v["custom_key"] != "preserved" || v["data_dir"] != "D:/keep-data" {
		t.Fatal("无关设置被覆盖")
	}
	if s.get().IntervalSec != 30 {
		t.Fatal("运行设置未发布")
	}
	s.cfg.configPath = filepath.Join(dir, "missing", "config.json")
	if err := s.save(screenshotSettings{40, 48}, false); err == nil {
		t.Fatal("写入失败不能返回成功")
	}
	if s.get().IntervalSec != 30 {
		t.Fatal("失败不应更新内存设置")
	}
}

func TestScreenshotSettingsHTTP(t *testing.T) {
	cfg := defaultConfig(t.TempDir())
	d := &dashboardServer{cfg: cfg, settings: newScreenshotSettingsStore(cfg)}
	h := d.handler()
	valid := `{"screenshot_interval_sec":120,"screenshot_retention_hours":48}`
	cases := []struct {
		method, origin, ctype, body string
		want                        int
	}{
		{"GET", "", "", "", 200},
		{"POST", "", "application/json", valid, 403},
		{"POST", "https://evil.example", "application/json", valid, 403},
		{"POST", "http://" + dashboardAddr, "text/plain", valid, 415},
		{"POST", "http://" + dashboardAddr, "application/json", `{"screenshot_interval_sec":0,"screenshot_retention_hours":48}`, 400},
		{"POST", "http://" + dashboardAddr, "application/json", `{"screenshot_interval_sec":60,"screenshot_retention_hours":0}`, 400},
		{"POST", "http://" + dashboardAddr, "application/json", valid + `{}`, 400},
		{"POST", "http://" + dashboardAddr, "application/json", `{"screenshot_interval_sec":60,"screenshot_retention_hours":12}`, 409},
		{"POST", "http://" + dashboardAddr, "application/json", valid, 200},
		{"POST", "http://" + dashboardAddr, "application/json", `{"screenshot_interval_sec":30,"screenshot_retention_hours":12,"confirm_shorter_retention":true}`, 200},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, "http://"+dashboardAddr+"/api/settings", strings.NewReader(c.body))
		r.Header.Set("Origin", c.origin)
		r.Header.Set("Content-Type", c.ctype)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Fatalf("%+v: %d %s", c, w.Code, w.Body.String())
		}
	}
}
