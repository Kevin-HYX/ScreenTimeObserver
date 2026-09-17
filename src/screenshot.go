package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"
)

var dpiAwarenessOnce sync.Once

// enablePerMonitorDPI 让虚拟屏幕尺寸使用物理像素，避免高 DPI 显示器被系统缩放后降采样。
func enablePerMonitorDPI() {
	dpiAwarenessOnce.Do(func() {
		// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 的句柄值是 -4。
		if ok, _, _ := procSetProcessDpiAwarenessContext.Call(^uintptr(3)); ok == 0 {
			procSetProcessDPIAware.Call()
		}
	})
}

const (
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79
	srccopy           = 0x00CC0020
	captureblt        = 0x40000000
	dibRGBColors      = 0
	biRGB             = 0
	cursorShowing     = 0x00000001
	diNormal          = 0x0003
)

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type cursorInfo struct {
	Size   uint32
	Flags  uint32
	Cursor uintptr
	Pos    point
}

type iconInfo struct {
	Icon     int32
	HotspotX uint32
	HotspotY uint32
	Mask     uintptr
	Color    uintptr
}

func (a *App) screenshotDir() string {
	return filepath.Join(a.cfg.DataDir, "screenshots")
}

// maybeMaintainScreenshots 每分钟安排截图、每十分钟安排过期清理。
// GDI 抓取和 PNG 编码在独立 goroutine 中完成，不阻塞窗口事件消息循环。
func (a *App) maybeMaintainScreenshots() {
	if !a.cfg.ScreenshotEnabled {
		return
	}
	now := time.Now()
	interval := time.Duration(a.cfg.ScreenshotIntervalSec * float64(time.Second))
	if interval <= 0 {
		interval = time.Minute
	}
	captureDue := a.lastScreenshotMono.IsZero() || now.Sub(a.lastScreenshotMono) >= interval
	cleanupDue := a.lastScreenshotCleanupMono.IsZero() || now.Sub(a.lastScreenshotCleanupMono) >= 10*time.Minute
	if !captureDue && !cleanupDue {
		return
	}
	if !a.screenshotBusy.CompareAndSwap(false, true) {
		return
	}
	if captureDue {
		a.lastScreenshotMono = now
	}
	if cleanupDue {
		a.lastScreenshotCleanupMono = now
	}

	a.screenshotWG.Add(1)
	go func(capturedAt time.Time, takeScreenshot, cleanOld bool) {
		defer a.screenshotWG.Done()
		defer a.screenshotBusy.Store(false)

		dir := a.screenshotDir()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			a.screenshotFailed("创建截图目录失败", err)
			return
		}
		if cleanOld {
			retention := time.Duration(a.cfg.ScreenshotRetentionHours * float64(time.Hour))
			removed, err := cleanupScreenshots(dir, capturedAt, retention)
			if err != nil {
				a.screenshotFailed("清理过期截图失败", err)
			}
			if removed > 0 {
				a.store.Log(fmt.Sprintf("已删除 %d 张超过保留期的截图", removed))
			}
		}
		if !takeScreenshot || a.screenshotPaused.Load() {
			return
		}

		path := filepath.Join(dir, screenshotFilename(capturedAt))
		if err := captureVirtualDesktopPNG(path); err != nil {
			a.screenshotFailed("桌面截图失败", err)
			return
		}
		// 若抓取期间恰好被人工暂停，不保留这张跨越暂停边界的截图。
		if a.screenshotPaused.Load() {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				a.screenshotFailed("删除暂停边界截图失败", err)
			}
			return
		}
		a.screenshotsWritten.Add(1)
		a.lastScreenshotTS.Store(capturedAt.Format("2006-01-02T15:04:05.000-07:00"))
		a.lastScreenshotPath.Store(path)
	}(now, captureDue, cleanupDue)
}

func (a *App) screenshotFailed(action string, err error) {
	a.screenshotFailures.Add(1)
	a.lastScreenshotFailure.Store(time.Now().UnixNano())
	a.store.Log(action + ": " + err.Error())
}

func screenshotFilename(t time.Time) string {
	return t.Format("20060102_150405.000_-0700") + ".png"
}

func cleanupScreenshots(dir string, now time.Time, retention time.Duration) (int, error) {
	if retention <= 0 {
		retention = 24 * time.Hour
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	cutoff := now.Add(-retention)
	removed := 0
	var firstErr error
	for _, entry := range entries {
		if entry.IsDir() || !managedScreenshotFile(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		removed++
	}
	return removed, firstErr
}

func managedScreenshotFile(name string) bool {
	if strings.HasPrefix(name, ".screenshot-") && strings.HasSuffix(name, ".tmp") {
		return true
	}
	_, err := time.Parse("20060102_150405.000_-0700.png", name)
	return err == nil
}

func virtualScreenBounds() (x, y, width, height int32) {
	xv, _, _ := procGetSystemMetrics.Call(smXVirtualScreen)
	yv, _, _ := procGetSystemMetrics.Call(smYVirtualScreen)
	wv, _, _ := procGetSystemMetrics.Call(smCXVirtualScreen)
	hv, _, _ := procGetSystemMetrics.Call(smCYVirtualScreen)
	return int32(xv), int32(yv), int32(wv), int32(hv)
}

// captureVirtualDesktopPNG 使用 Windows 虚拟屏幕矩形，多显示器会按系统布局落在同一张原尺寸 PNG 中。
func captureVirtualDesktopPNG(path string) error {
	enablePerMonitorDPI()
	x, y, width, height := virtualScreenBounds()
	if width <= 0 || height <= 0 {
		return fmt.Errorf("虚拟屏幕尺寸无效: %dx%d", width, height)
	}
	screenDC, _, err := procGetDC.Call(0)
	if screenDC == 0 {
		return fmt.Errorf("GetDC: %w", err)
	}
	defer procReleaseDC.Call(0, screenDC)
	memDC, _, err := procCreateCompatibleDC.Call(screenDC)
	if memDC == 0 {
		return fmt.Errorf("CreateCompatibleDC: %w", err)
	}
	defer procDeleteDC.Call(memDC)
	bitmap, _, err := procCreateCompatibleBitmap.Call(screenDC, uintptr(width), uintptr(height))
	if bitmap == 0 {
		return fmt.Errorf("CreateCompatibleBitmap: %w", err)
	}
	defer procDeleteObject.Call(bitmap)
	old, _, err := procSelectObject.Call(memDC, bitmap)
	if old == 0 {
		return fmt.Errorf("SelectObject: %w", err)
	}
	defer procSelectObject.Call(memDC, old)
	if ok, _, err := procBitBlt.Call(memDC, 0, 0, uintptr(width), uintptr(height), screenDC,
		uintptr(x), uintptr(y), srccopy|captureblt); ok == 0 {
		return fmt.Errorf("BitBlt: %w", err)
	}
	drawCursor(memDC, x, y)

	pixels := make([]byte, int(width)*int(height)*4)
	header := bitmapInfoHeader{
		Size: uint32(unsafe.Sizeof(bitmapInfoHeader{})), Width: width, Height: -height,
		Planes: 1, BitCount: 32, Compression: biRGB, SizeImage: uint32(len(pixels)),
	}
	rows, _, err := procGetDIBits.Call(memDC, bitmap, 0, uintptr(height),
		uintptr(unsafe.Pointer(&pixels[0])), uintptr(unsafe.Pointer(&header)), dibRGBColors)
	runtime.KeepAlive(pixels)
	runtime.KeepAlive(header)
	if rows != uintptr(height) {
		return fmt.Errorf("GetDIBits: 读取 %d/%d 行: %w", rows, height, err)
	}
	for i := 0; i < len(pixels); i += 4 {
		pixels[i], pixels[i+2] = pixels[i+2], pixels[i]
		pixels[i+3] = 0xff
	}
	img := &image.RGBA{Pix: pixels, Stride: int(width) * 4, Rect: image.Rect(0, 0, int(width), int(height))}
	return writePNGAtomic(path, img)
}

func drawCursor(dc uintptr, virtualX, virtualY int32) {
	ci := cursorInfo{Size: uint32(unsafe.Sizeof(cursorInfo{}))}
	if ok, _, _ := procGetCursorInfo.Call(uintptr(unsafe.Pointer(&ci))); ok == 0 || ci.Flags&cursorShowing == 0 || ci.Cursor == 0 {
		return
	}
	ii := iconInfo{}
	if ok, _, _ := procGetIconInfo.Call(ci.Cursor, uintptr(unsafe.Pointer(&ii))); ok == 0 {
		return
	}
	if ii.Mask != 0 {
		procDeleteObject.Call(ii.Mask)
	}
	if ii.Color != 0 {
		procDeleteObject.Call(ii.Color)
	}
	dx := ci.Pos.x - virtualX - int32(ii.HotspotX)
	dy := ci.Pos.y - virtualY - int32(ii.HotspotY)
	procDrawIconEx.Call(dc, uintptr(dx), uintptr(dy), ci.Cursor, 0, 0, 0, 0, diNormal)
}

func writePNGAtomic(path string, img image.Image) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".screenshot-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		tmp.Close()
		if !ok {
			os.Remove(tmpPath)
		}
	}()
	if err := png.Encode(tmp, img); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	ok = true
	return nil
}
