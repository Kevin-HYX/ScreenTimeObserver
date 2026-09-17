package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScreenshotFilenameContainsSortableTime(t *testing.T) {
	tm := time.Date(2026, 9, 15, 21, 45, 7, 123000000, time.FixedZone("CST", 8*3600))
	got := screenshotFilename(tm)
	if got != "20260915_214507.123_+0800.png" {
		t.Fatalf("截图文件名不符合预期: %s", got)
	}
}

func TestCleanupScreenshotsKeepsOnly24Hours(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	oldPath := filepath.Join(dir, screenshotFilename(now.Add(-25*time.Hour)))
	freshPath := filepath.Join(dir, screenshotFilename(now.Add(-23*time.Hour)))
	unrelatedPath := filepath.Join(dir, "用户自己的文件.png")
	if err := os.WriteFile(oldPath, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(freshPath, []byte("fresh"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unrelatedPath, []byte("unrelated"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(oldPath, now.Add(-25*time.Hour), now.Add(-25*time.Hour))
	os.Chtimes(freshPath, now.Add(-23*time.Hour), now.Add(-23*time.Hour))
	os.Chtimes(unrelatedPath, now.Add(-25*time.Hour), now.Add(-25*time.Hour))
	removed, err := cleanupScreenshots(dir, now, 24*time.Hour)
	if err != nil || removed != 1 {
		t.Fatalf("清理结果异常: removed=%d err=%v", removed, err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatal("超过 24 小时的截图仍存在")
	}
	if _, err := os.Stat(freshPath); err != nil {
		t.Fatal("24 小时内的截图被误删")
	}
	if _, err := os.Stat(unrelatedPath); err != nil {
		t.Fatal("截图目录内非采集器文件被误删")
	}
}

func TestCaptureVirtualDesktop(t *testing.T) {
	if os.Getenv("SCT_SCREENSHOT_TEST") != "1" {
		t.Skip("设置 SCT_SCREENSHOT_TEST=1 才抓取真实桌面")
	}
	path := filepath.Join(t.TempDir(), "desktop.png")
	if err := captureVirtualDesktopPNG(path); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, err := png.DecodeConfig(f)
	if err != nil {
		t.Fatal(err)
	}
	_, _, width, height := virtualScreenBounds()
	t.Logf("虚拟桌面与 PNG 尺寸: %dx%d", width, height)
	if cfg.Width != int(width) || cfg.Height != int(height) {
		t.Fatalf("PNG 尺寸 %dx%d，不是虚拟桌面 %dx%d", cfg.Width, cfg.Height, width, height)
	}
}

func TestPausedCollectorCleansButDoesNotCapture(t *testing.T) {
	root := t.TempDir()
	cfg := defaultConfig(root)
	cfg.DataDir = filepath.Join(root, "data")
	cfg.LogDir = filepath.Join(root, "logs")
	store, err := newStore(cfg.DataDir, cfg.LogDir)
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(cfg, root, true, false)
	a.store = store
	a.screenshotPaused.Store(true)
	dir := a.screenshotDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(dir, screenshotFilename(time.Now().Add(-25*time.Hour)))
	if err := os.WriteFile(oldPath, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(oldPath, time.Now().Add(-25*time.Hour), time.Now().Add(-25*time.Hour))

	a.maybeMaintainScreenshots()
	a.screenshotWG.Wait()
	store.Close()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 || a.screenshotsWritten.Load() != 0 {
		t.Fatalf("暂停期间不应截图，但目录为 %v，计数为 %d", entries, a.screenshotsWritten.Load())
	}
}
