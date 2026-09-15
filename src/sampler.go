package main

import (
	"math"
	"path/filepath"
	"syscall"
	"unsafe"
)

// Sample 是一次前台窗口采样结果。
type Sample struct {
	Process     string
	PID         uint32
	Title       string
	WindowClass string
}

func sampleForeground() Sample {
	var s Sample
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return s
	}
	var pid uint32
	procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	s.PID = pid
	s.Title = windowString(hwnd, procGetWindowTextW, 1024)
	s.WindowClass = windowString(hwnd, procGetClassNameW, 256)
	if pid == 0 {
		return s
	}
	h, _, _ := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if h == 0 {
		return s
	}
	defer procCloseHandle.Call(h)
	buf := make([]uint16, 1024)
	size := uint32(len(buf))
	ok, _, _ := procQueryFullProcessImageNameW.Call(h, 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if ok != 0 && size > 0 {
		s.Process = filepath.Base(syscall.UTF16ToString(buf[:size]))
	}
	return s
}

// idleSeconds 返回距上次键鼠输入的秒数（由系统维护的输入时间戳给出）。
// 取不到时返回 NaN，绝不猜 0。
func idleSeconds() float64 {
	li := lastInputInfo{cbSize: uint32(unsafe.Sizeof(lastInputInfo{}))}
	ok, _, _ := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&li)))
	if ok == 0 {
		return math.NaN()
	}
	tick, _, _ := procGetTickCount.Call()
	// dwTime 与 GetTickCount 同为 32 位，无符号相减自然处理回绕
	ms := uint32(tick) - li.dwTime
	return math.Round(float64(ms)/100.0) / 10.0
}

// sessionLocked 用能否打开输入桌面判断是否处于锁屏界面。
func sessionLocked() bool {
	h, _, _ := procOpenInputDesktop.Call(0, 0, desktopSwitchDesktop)
	if h == 0 {
		return true
	}
	procCloseDesktop.Call(h)
	return false
}

func nanSeconds(v float64) *float64 {
	if math.IsNaN(v) {
		return nil
	}
	out := v
	return &out
}

func floatPtr(v float64) *float64 {
	out := v
	return &out
}
