package main

import (
	"encoding/json"
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
