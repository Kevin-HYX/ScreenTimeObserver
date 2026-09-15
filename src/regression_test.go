package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPausedLifecycleHasNoDetails(t *testing.T) {
	dir := t.TempDir()
	s, err := newStore(filepath.Join(dir, "data"), filepath.Join(dir, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(defaultConfig(dir), dir, true, false)
	a.store = s
	a.paused = true
	a.fg = Sample{Process: "secret.exe", PID: 123, Title: "private", WindowClass: "private"}
	for _, event := range []string{"start", "carry_over", "offline", "online", "stop"} {
		r := a.stateRecord(event, "test")
		if !r.Paused || r.Process != "" || r.PID != 0 || r.Title != "" || r.WindowClass != "" || r.IdleSec != nil {
			t.Fatalf("暂停事件泄漏: %+v", r)
		}
	}
	a.fg = Sample{}
	a.syncPause()
	s.Close()
	b, err := os.ReadFile(filepath.Join(dir, "data", time.Now().Format("2006-01-02")+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var r Record
	if err = json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if r.Event != "resumed" || r.Paused {
		t.Fatalf("恢复状态错误: %+v", r)
	}
}

func TestLastEpochAcrossDays(t *testing.T) {
	dir := t.TempDir()
	day := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	os.WriteFile(filepath.Join(dir, day+".jsonl"), []byte("{\"epoch\":123}\n"), 0600)
	if got := lastRecordEpoch(dir); got != 123 {
		t.Fatalf("got %v", got)
	}
}

func TestCancelledShutdownKeepsCollectorAlive(t *testing.T) {
	a := &App{}
	if a.wndProc(0, wmQueryEndSession, 0, 0) != 1 || a.quitFlag {
		t.Fatal("询问阶段不应退出")
	}
	a.wndProc(0, wmEndSession, 0, 0)
	if a.quitFlag {
		t.Fatal("取消关机不应退出")
	}
}

func TestReviewDoesNotAttributeGapOrHistoricalTail(t *testing.T) {
	dir := t.TempDir()
	day := "2026-01-01"
	records := []Record{{TS: day + "T00:00:00+08:00", Epoch: 100, Event: "start", Process: "test.exe"}, {TS: day + "T00:05:00+08:00", Epoch: 400, Event: "heartbeat", Process: "test.exe"}}
	f, err := os.Create(filepath.Join(dir, day+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		json.NewEncoder(f).Encode(r)
	}
	f.Close()
	out, err := os.CreateTemp(t.TempDir(), "review")
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = out
	cfg := defaultConfig(dir)
	cfg.DataDir = dir
	result := cmdReview(cfg, day, true)
	os.Stdout = old
	out.Close()
	if result != 0 {
		t.Fatal("复看失败")
	}
	b, _ := os.ReadFile(out.Name())
	var v struct {
		OpenTail float64   `json:"open_tail_sec"`
		Top      []nameSec `json:"top_processes"`
		Kinds    []nameSec `json:"by_kind"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if v.OpenTail != 0 || len(v.Top) != 0 || len(v.Kinds) != 1 || v.Kinds[0].Name != "unknown" {
		t.Fatalf("空洞或历史尾部被误计: %s", b)
	}
}

// TestClipboardWrite 只在显式开启时运行：它会覆盖当前剪贴板内容。
func TestClipboardWrite(t *testing.T) {
	if os.Getenv("SCT_CLIPBOARD_TEST") != "1" {
		t.Skip("设置 SCT_CLIPBOARD_TEST=1 才运行，避免动到用户剪贴板")
	}
	root := "D:" + string(os.PathSeparator)
	want := filepath.Join(root, "ScreenTimeObserver", "data")
	if err := setClipboardText(want); err != nil {
		t.Fatalf("写入剪贴板失败: %v", err)
	}
	t.Log("已写入剪贴板:", want)
}

// TestAudioDetection 需要真的在系统里播放或停掉声音，所以默认跳过。
// 运行方式：设置 SCT_AUDIO_EXPECT=playing 或 silent。
func TestAudioDetection(t *testing.T) {
	expect := os.Getenv("SCT_AUDIO_EXPECT")
	if expect == "" {
		t.Skip("设置 SCT_AUDIO_EXPECT=playing 或 silent 才运行")
	}
	if err := comInit(); err != nil {
		t.Fatalf("%v", err)
	}
	m := &audioMeter{}
	defer m.close()
	peak, err := m.peak()
	if err != nil {
		t.Fatalf("读取音频峰值失败: %v", err)
	}
	t.Logf("峰值 = %v（阈值 %v）", peak, audioPeakThreshold)
	switch expect {
	case "playing":
		if peak <= audioPeakThreshold {
			t.Fatalf("应该在播放，但峰值为 %v", peak)
		}
	case "silent":
		if peak > audioPeakThreshold {
			t.Fatalf("应该是静音，但峰值为 %v", peak)
		}
	default:
		t.Fatalf("SCT_AUDIO_EXPECT 只能是 playing 或 silent，收到 %q", expect)
	}
}

// TestMediaCountsAsPresence 检查「有人看视频但没动键鼠」这一类时间：
// 单独记为 media，同时计入该应用的占用。
func TestMediaCountsAsPresence(t *testing.T) {
	base := float64(1767315600)
	var recs []Record
	for i := 0; i < 4; i++ {
		recs = append(recs, Record{
			TS:      "2026-01-02T09:00:00.000+08:00",
			Epoch:   base + float64(i*60),
			Event:   "heartbeat",
			Reason:  "heartbeat",
			Session: "active",
			Process: "potplayer.exe",
			Title:   "电影",
			Media:   true,
			IdleSec: floatPtr(600),
		})
	}
	a := analyzeDay("2026-01-02", recs, 0, "x.jsonl", 90)
	if len(a.ByKind) != 1 || a.ByKind[0].Name != "media" || a.ByKind[0].Sec != 180 {
		t.Fatalf("media 未单独成类: %v", a.ByKind)
	}
	if len(a.ByProcess) != 1 || a.ByProcess[0].Name != "potplayer.exe" || a.ByProcess[0].Sec != 180 {
		t.Fatalf("观看时间未计入应用占用: %v", a.ByProcess)
	}
	if a.HoleSec != 0 || a.CoveredSec != 180 {
		t.Fatalf("观看不应算作空洞: covered=%v hole=%v", a.CoveredSec, a.HoleSec)
	}
}

func TestIdleState(t *testing.T) {
	cases := []struct {
		name    string
		idleSec float64
		audio   bool
		idle    bool
		media   bool
	}{
		{"刚开始用", 5, false, false, false},
		{"刚离开但没声音", 200, false, true, false},
		{"看视频没动键鼠", 200, true, false, true},
		{"正在操作且有背景音乐", 1, true, false, false},
		{"刚好到阈值", 180, false, true, false},
		{"取不到空闲时间", math.NaN(), false, false, false},
	}
	for _, c := range cases {
		idle, media := idleState(c.idleSec, 180, c.audio)
		if idle != c.idle || media != c.media {
			t.Errorf("%s: 期望 idle=%v media=%v，实际 idle=%v media=%v", c.name, c.idle, c.media, idle, media)
		}
	}
}
