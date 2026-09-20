package main

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed dashboard/*
var dashboardAssets embed.FS

const dashboardAddr = "127.0.0.1:17643"

type dashboardServer struct {
	settings *screenshotSettingsStore
	server   *http.Server
	cfg      Config
	mu       sync.Mutex
	cache    map[string]dashboardCache
}
type dashboardCache struct {
	at  time.Time
	day dashboardDay
}
type dashboardDay struct {
	Date     string             `json:"date"`
	Start    float64            `json:"start"`
	End      float64            `json:"end"`
	Until    float64            `json:"until"`
	BadLines int                `json:"bad_lines"`
	Segments []segRow           `json:"segments"`
	Totals   map[string]float64 `json:"totals"`
	Apps     []nameSec          `json:"apps"`
	Shots    []dashboardShot    `json:"shots"`
}
type dashboardShot struct {
	TS   float64 `json:"ts"`
	File string  `json:"file"`
}

// 时间轴覆盖整个已发生的日期范围；所有汇总都从这些不重叠区间导出。
func buildDashboardDay(date string, recs []Record, bad int, now time.Time, live bool, gap float64) dashboardDay {
	startTime, _ := time.ParseInLocation("2006-01-02", date, time.Local)
	start := float64(startTime.Unix())
	end := float64(startTime.AddDate(0, 0, 1).Unix())
	until := math.Max(start, math.Min(end, float64(now.UnixMilli())/1000))
	d := dashboardDay{Date: date, Start: start, End: end, Until: until, BadLines: bad, Segments: []segRow{}, Totals: map[string]float64{}, Apps: []nameSec{}, Shots: []dashboardShot{}}
	if gap <= 0 {
		gap = 90
	}
	// 同时钟时间的记录保留最后一条，倒序时间戳单独计入数据质量提示。
	clean := make([]Record, 0, len(recs))
	for _, r := range recs {
		if math.IsNaN(r.Epoch) || math.IsInf(r.Epoch, 0) || r.Epoch <= 0 {
			d.BadLines++
			continue
		}
		if len(clean) > 0 && r.Epoch < clean[len(clean)-1].Epoch {
			d.BadLines++
		}
		clean = append(clean, r)
	}
	sort.SliceStable(clean, func(i, j int) bool { return clean[i].Epoch < clean[j].Epoch })
	recs = clean
	cursor := start
	appendSegment := func(s segRow) {
		if s.End <= s.Start {
			return
		}
		s.Dur = s.End - s.Start
		if s.Kind != "active" && s.Kind != "media" {
			s.Process = ""
			s.Title = ""
		}
		n := len(d.Segments)
		if n > 0 {
			p := &d.Segments[n-1]
			if p.End == s.Start && p.Kind == s.Kind && p.Process == s.Process && p.Title == s.Title {
				p.End = s.End
				p.Dur = p.End - p.Start
				return
			}
		}
		d.Segments = append(d.Segments, s)
	}
	for i, r := range recs {
		next := r.Epoch
		if i+1 < len(recs) {
			next = recs[i+1].Epoch
		} else if live && date == now.Format("2006-01-02") && until-r.Epoch <= gap && r.Event != "stop" {
			next = until
		}
		left := math.Max(start, r.Epoch)
		right := math.Min(until, next)
		if right <= left {
			continue
		}
		if left > cursor {
			appendSegment(segRow{Start: cursor, End: left, Kind: "unknown"})
		}
		kind := segmentKind(r, next-r.Epoch, gap)
		// 进程重新启动不能证明此前一直处于同一个状态。
		if i+1 < len(recs) && recs[i+1].Event == "start" && kind != "suspended" {
			kind = "unknown"
		}
		appendSegment(segRow{Start: left, End: right, Kind: kind, Process: r.Process, Title: r.Title, Reason: r.Reason, Event: r.Event})
		cursor = right
	}
	if cursor < until {
		appendSegment(segRow{Start: cursor, End: until, Kind: "unknown"})
	}
	apps := map[string]float64{}
	for _, s := range d.Segments {
		d.Totals[s.Kind] += s.Dur
		if s.Kind == "active" || s.Kind == "media" {
			name := s.Process
			if name == "" {
				name = "未识别应用"
			}
			apps[name] += s.Dur
		}
	}
	d.Apps = sortedSec(apps)
	return d
}

func (d *dashboardServer) day(date string) (dashboardDay, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	if c, ok := d.cache[date]; ok && now.Sub(c.at) < 10*time.Second {
		return c.day, nil
	}
	recs, bad, _, err := loadDayRecords(d.cfg.DataDir, date)
	if err != nil && !os.IsNotExist(err) {
		return dashboardDay{}, err
	}
	dayTime, _ := time.ParseInLocation("2006-01-02", date, time.Local)
	// 相邻日提供跨午夜的观测边界，不凭空延伸历史尾部。
	previous, _, _, e := loadDayRecords(d.cfg.DataDir, dayTime.AddDate(0, 0, -1).Format("2006-01-02"))
	if e != nil && !os.IsNotExist(e) {
		return dashboardDay{}, e
	}
	if len(previous) > 0 {
		recs = append([]Record{previous[len(previous)-1]}, recs...)
	}
	next, _, _, e := loadDayRecords(d.cfg.DataDir, dayTime.AddDate(0, 0, 1).Format("2006-01-02"))
	if e != nil && !os.IsNotExist(e) {
		return dashboardDay{}, e
	}
	if len(next) > 0 {
		recs = append(recs, next[0])
	}
	var status struct {
		Updated string `json:"updated"`
		PID     int    `json:"pid"`
	}
	b, e := os.ReadFile(filepath.Join(d.cfg.LogDir, "status.json"))
	live := false
	if e == nil && json.Unmarshal(b, &status) == nil {
		at, e := time.Parse(time.RFC3339, status.Updated)
		live = e == nil && status.PID == os.Getpid() && now.Sub(at) >= 0 && now.Sub(at) < 35*time.Second
	}
	result := buildDashboardDay(date, recs, bad, now, live, d.cfg.GapReportSec)
	entries, e := os.ReadDir(filepath.Join(d.cfg.DataDir, "screenshots"))
	if e != nil && !os.IsNotExist(e) {
		return dashboardDay{}, e
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		t, e := time.Parse("20060102_150405.000_-0700.png", entry.Name())
		if e != nil {
			continue
		}
		ts := float64(t.UnixMilli()) / 1000
		if ts >= result.Start && ts < result.End {
			result.Shots = append(result.Shots, dashboardShot{ts, entry.Name()})
		}
	}
	sort.Slice(result.Shots, func(i, j int) bool { return result.Shots[i].TS < result.Shots[j].TS })
	if len(d.cache) > 14 {
		clear(d.cache)
	}
	d.cache[date] = dashboardCache{now, result}
	return result, nil
}

func (d *dashboardServer) handler() http.Handler {
	assets, _ := fs.Sub(dashboardAssets, "dashboard")
	mux := http.NewServeMux()
	mux.HandleFunc("/api/settings", d.handleSettings)
	mux.HandleFunc("/api/day", func(w http.ResponseWriter, r *http.Request) {
		date := r.URL.Query().Get("date")
		if !validDate(date) {
			http.Error(w, "日期格式应为 YYYY-MM-DD", 400)
			return
		}
		result, err := d.day(date)
		if err != nil {
			http.Error(w, "读取该日数据失败", 500)
			return
		}
		writeDashboardJSON(w, result)
	})
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		b, err := os.ReadFile(filepath.Join(d.cfg.LogDir, "status.json"))
		if err != nil {
			http.Error(w, "状态暂不可用", 503)
			return
		}
		var v map[string]any
		if json.Unmarshal(b, &v) != nil {
			http.Error(w, "状态暂不可用", 503)
			return
		}
		writeDashboardJSON(w, map[string]any{"updated": v["updated"], "paused": v["paused"], "hook_ok": v["hook_ok"], "today": today(), "dates": dayFiles(d.cfg.DataDir)})
	})
	mux.HandleFunc("/shot/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/shot/")
		if _, err := time.Parse("20060102_150405.000_-0700.png", name); err != nil || filepath.Base(name) != name {
			http.NotFound(w, r)
			return
		}
		path := filepath.Join(d.cfg.DataDir, "screenshots", name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, path)
	})
	mux.Handle("/", http.FileServer(http.FS(assets)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 拒绝 DNS 重绑定和外站读取本机窗口标题、截图。
		if r.Host != dashboardAddr {
			http.Error(w, "仅允许本机仪表盘访问", 403)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+dashboardAddr {
			http.Error(w, "来源不允许", 403)
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
			http.Error(w, "来源不允许", 403)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !(r.URL.Path == "/api/settings" && r.Method == http.MethodPost) {
			http.Error(w, "仅支持读取", 405)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' blob:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		mux.ServeHTTP(w, r)
	})
}
func writeDashboardJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func (a *App) startDashboard() {
	listener, err := net.Listen("tcp4", dashboardAddr)
	if err != nil {
		a.store.Log("仪表盘启动失败: " + err.Error())
		return
	}
	d := &dashboardServer{cfg: a.cfg, settings: a.settings, cache: map[string]dashboardCache{}}
	d.server = &http.Server{Handler: d.handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	a.dashboard = d
	go func() {
		if err := d.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.store.Log("仪表盘服务异常: " + err.Error())
		}
	}()
	a.store.Log("仪表盘已启动 http://" + dashboardAddr)
}
func (a *App) openDashboard() {
	if a.dashboard == nil {
		a.startDashboard()
	}
	if a.dashboard == nil {
		a.notify("仪表盘无法打开", "本地端口被占用，请查看运行日志")
		return
	}
	// Windows URL 协议处理程序遵循用户设置的默认浏览器。
	cmd := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", "http://"+dashboardAddr+"/")
	if err := cmd.Start(); err != nil {
		a.store.Log(fmt.Sprintf("打开仪表盘失败: %v", err))
		a.notify("打开失败", "请在浏览器访问 http://"+dashboardAddr)
		return
	}
	_ = cmd.Process.Release()
}
