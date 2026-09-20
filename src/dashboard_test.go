package main

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDashboardIntervalsAndTotals(t *testing.T) {
	date := "2026-09-17"
	base, _ := dayStartEpoch(date)
	records := []Record{
		{Epoch: base + 60, Process: "a.exe", Title: "工作"},
		{Epoch: base + 120, Process: "a.exe", Title: "工作", Media: true},
		{Epoch: base + 180, Paused: true, Title: "不应泄露"},
		{Epoch: base + 240, Session: "suspended"},
		{Epoch: base + 3600, Process: "b.exe"},
		{Epoch: base + 3900, Process: "b.exe"},
		{Epoch: base + 3960, Event: "stop"},
	}
	d := buildDashboardDay(date, records, 0, time.Unix(int64(base+4000), 0), true, 90)
	if d.Totals["active"] != 120 || d.Totals["media"] != 60 || d.Totals["paused"] != 60 || d.Totals["suspended"] != 3360 || d.Totals["unknown"] != 400 {
		t.Fatalf("错误统计: %+v", d.Totals)
	}
	total := 0.0
	cursor := d.Start
	for _, s := range d.Segments {
		if s.Start != cursor {
			t.Fatalf("重叠或遗漏: %+v", s)
		}
		cursor = s.End
		total += s.Dur
		if s.Kind == "paused" && s.Title != "" {
			t.Fatal("暂停泄露标题")
		}
	}
	if total != 4000 || cursor != d.Until {
		t.Fatalf("区间不守恒: %v", total)
	}
	apps := 0.0
	for _, a := range d.Apps {
		apps += a.Sec
	}
	if apps != d.Totals["active"]+d.Totals["media"] {
		t.Fatal("排行与概览不一致")
	}
}
func TestDashboardMidnightAndTail(t *testing.T) {
	date := "2026-09-17"
	base, _ := dayStartEpoch(date)
	recs := []Record{{Epoch: base - 30, Process: "a"}, {Epoch: base + 30, Process: "a"}, {Epoch: base + 86400 - 30, Process: "b"}, {Epoch: base + 86400 + 30, Process: "b"}}
	d := buildDashboardDay(date, recs, 0, time.Unix(int64(base+172800), 0), false, 90)
	if d.Totals["active"] != 60 {
		t.Fatalf("跨日分摊错误: %+v", d.Totals)
	}
	if d.Segments[0].Start != base || d.Segments[len(d.Segments)-1].End != base+86400 {
		t.Fatal("日期边界错误")
	}
	for _, live := range []bool{false, true} {
		d = buildDashboardDay(date, []Record{{Epoch: base + 100, Process: "a"}}, 0, time.Unix(int64(base+120), 0), live, 90)
		want := 0.0
		if live {
			want = 20
		}
		if d.Totals["active"] != want {
			t.Fatalf("在线尾段错误: live=%v %+v", live, d.Totals)
		}
	}
	d = buildDashboardDay(date, []Record{{Epoch: base + 100, Process: "a"}}, 0, time.Unix(int64(base+500), 0), true, 90)
	if d.Totals["active"] != 0 {
		t.Fatal("过期尾段被延伸")
	}
}
func TestDashboardEmptyRestartAndMerge(t *testing.T) {
	date := "2026-09-17"
	base, _ := dayStartEpoch(date)
	now := time.Unix(int64(base+300), 0)
	d := buildDashboardDay(date, nil, 0, now, false, 90)
	if len(d.Segments) != 1 || d.Totals["unknown"] != 300 {
		t.Fatal("空日错误")
	}
	recs := []Record{{Epoch: base, Process: "a"}, {Epoch: base + 60, Process: "a", Event: "start"}, {Epoch: base + 120, Process: "a"}, {Epoch: base + 180, Process: "a"}}
	d = buildDashboardDay(date, recs, 0, now, false, 90)
	if d.Totals["unknown"] != 180 || len(d.Segments) != 3 {
		t.Fatalf("重启或心跳合并错误: %+v", d)
	}
}
func TestDashboardHTTPPrivacyAndFiles(t *testing.T) {
	root := t.TempDir()
	cfg := defaultConfig(root)
	os.MkdirAll(cfg.DataDir, 0700)
	os.MkdirAll(cfg.LogDir, 0700)
	os.MkdirAll(filepath.Join(cfg.DataDir, "screenshots"), 0700)
	shot := "20260917_120000.000_+0800.png"
	os.WriteFile(filepath.Join(cfg.DataDir, "screenshots", shot), []byte("image"), 0600)
	os.WriteFile(filepath.Join(cfg.DataDir, "2026-09-17.jsonl"), []byte("bad-line\n"), 0600)
	d := &dashboardServer{cfg: cfg, cache: map[string]dashboardCache{}}
	h := d.handler()
	cases := []struct {
		path, host, origin, site, method string
		want                             int
	}{
		{"/", dashboardAddr, "", "", "GET", 200}, {"/app.js", dashboardAddr, "", "", "GET", 200},
		{"/api/day?date=2026-09-17", dashboardAddr, "", "", "GET", 200},
		{"/api/day?date=../../logs", dashboardAddr, "", "", "GET", 400},
		{"/api/day?date=2026-09-17", "evil.example", "", "", "GET", 403},
		{"/api/day?date=2026-09-17", dashboardAddr, "https://evil.example", "", "GET", 403},
		{"/shot/" + shot, dashboardAddr, "", "cross-site", "GET", 403},
		{"/shot/" + shot, dashboardAddr, "", "", "GET", 200},
		{"/shot/secret.png", dashboardAddr, "", "", "GET", 404},
		{"/api/day?date=2026-09-17", dashboardAddr, "", "", "POST", 405},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, "http://"+c.host+c.path, nil)
		r.Host = c.host
		r.Header.Set("Origin", c.origin)
		r.Header.Set("Sec-Fetch-Site", c.site)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s %s => %d want %d", c.host, c.path, w.Code, c.want)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "http://"+dashboardAddr+"/api/day?date=2026-09-17", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var day dashboardDay
	if json.Unmarshal(w.Body.Bytes(), &day) != nil || day.BadLines != 1 || len(day.Shots) != 1 {
		t.Fatalf("接口数据错误: %s", w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("缺少来源隔离")
	}
	fr := httptest.NewRequest(http.MethodGet, "http://"+dashboardAddr+"/favicon.svg", nil)
	fw := httptest.NewRecorder()
	h.ServeHTTP(fw, fr)
	if fw.Code != 200 {
		t.Fatalf("favicon.svg => %d want 200", fw.Code)
	}
	if ct := fw.Header().Get("Content-Type"); ct != "image/svg+xml" {
		t.Fatalf("favicon Content-Type = %q，nosniff 下类型不对会静默不显示", ct)
	}
	icon := fw.Body.String()
	if !strings.Contains(icon, "#4B90E8") || !strings.Contains(icon, "M96 141v21M59 162h74") {
		t.Fatal("favicon 不是托盘图标的几何图形")
	}
}
func TestDashboardNonFiniteAndDuplicates(t *testing.T) {
	base, _ := dayStartEpoch("2026-09-17")
	d := buildDashboardDay("2026-09-17", []Record{{Epoch: base}, {Epoch: base, Process: "last"}, {Epoch: math.Inf(1)}, {Epoch: base + 60}}, 0, time.Unix(int64(base+60), 0), false, 90)
	if d.BadLines != 1 || len(d.Apps) != 1 || d.Apps[0].Name != "last" {
		t.Fatalf("非法时间或重复时间处理错误: %+v", d)
	}
}
