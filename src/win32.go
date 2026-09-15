package main

import (
	"syscall"
	"unsafe"
)

// 全部通过纯标准库绑定，不引入任何第三方依赖。

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")
)

var (
	procGetForegroundWindow        = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessID   = user32.NewProc("GetWindowThreadProcessId")
	procGetWindowTextW             = user32.NewProc("GetWindowTextW")
	procGetClassNameW              = user32.NewProc("GetClassNameW")
	procGetLastInputInfo           = user32.NewProc("GetLastInputInfo")
	procOpenInputDesktop           = user32.NewProc("OpenInputDesktop")
	procCloseDesktop               = user32.NewProc("CloseDesktop")
	procSetWinEventHook            = user32.NewProc("SetWinEventHook")
	procUnhookWinEvent             = user32.NewProc("UnhookWinEvent")
	procRegisterClassExW           = user32.NewProc("RegisterClassExW")
	procCreateWindowExW            = user32.NewProc("CreateWindowExW")
	procDefWindowProcW             = user32.NewProc("DefWindowProcW")
	procDestroyWindow              = user32.NewProc("DestroyWindow")
	procSetTimer                   = user32.NewProc("SetTimer")
	procKillTimer                  = user32.NewProc("KillTimer")
	procLoadIconW                  = user32.NewProc("LoadIconW")
	procCreatePopupMenu            = user32.NewProc("CreatePopupMenu")
	procAppendMenuW                = user32.NewProc("AppendMenuW")
	procDestroyMenu                = user32.NewProc("DestroyMenu")
	procTrackPopupMenu             = user32.NewProc("TrackPopupMenu")
	procGetCursorPos               = user32.NewProc("GetCursorPos")
	procGetMessageW                = user32.NewProc("GetMessageW")
	procPeekMessageW               = user32.NewProc("PeekMessageW")
	procTranslateMessage           = user32.NewProc("TranslateMessage")
	procDispatchMessageW           = user32.NewProc("DispatchMessageW")
	procPostQuitMessage            = user32.NewProc("PostQuitMessage")
	procPostMessageW               = user32.NewProc("PostMessageW")
	procSetForegroundWindow        = user32.NewProc("SetForegroundWindow")
	procShellNotifyIconW           = shell32.NewProc("Shell_NotifyIconW")
	procOpenProcess                = kernel32.NewProc("OpenProcess")
	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
	procCloseHandle                = kernel32.NewProc("CloseHandle")
	procCreateMutexW               = kernel32.NewProc("CreateMutexW")
	procGetModuleHandleW           = kernel32.NewProc("GetModuleHandleW")
	procGetCurrentProcess          = kernel32.NewProc("GetCurrentProcess")
	procGetProcessTimes            = kernel32.NewProc("GetProcessTimes")
	procGetTickCount               = kernel32.NewProc("GetTickCount")
	procGetProcessHandleCount      = kernel32.NewProc("GetProcessHandleCount")
	procGetTickCount64             = kernel32.NewProc("GetTickCount64")
	procGetGuiResources            = user32.NewProc("GetGuiResources")
	procRegisterWindowMessageW     = user32.NewProc("RegisterWindowMessageW")
	procLoadImageW                 = user32.NewProc("LoadImageW")
	procGetSystemMetrics           = user32.NewProc("GetSystemMetrics")
	procDestroyIcon                = user32.NewProc("DestroyIcon")
	procRegOpenKeyExW              = advapi32.NewProc("RegOpenKeyExW")
	procRegQueryValueExW           = advapi32.NewProc("RegQueryValueExW")
	procRegCloseKey                = advapi32.NewProc("RegCloseKey")
)

const (
	processQueryLimitedInformation = 0x1000
	desktopSwitchDesktop           = 0x0100
	errorAlreadyExists             = 183
)

const (
	wmDestroy         = 0x0002
	wmClose           = 0x0010
	wmQueryEndSession = 0x0011
	wmEndSession      = 0x0016
	wmPowerBroadcast  = 0x0218
	wmTimer           = 0x0113
	wmApp             = 0x8000
	wmLButtonUp       = 0x0202
	wmRButtonUp       = 0x0205
	wmLButtonDblClk   = 0x0203
	wmNull            = 0x0000

	wmTrayIcon  = wmApp + 1
	timerTickID = 1

	idTrayPause  = 1001
	idTrayOpen   = 1002
	idTrayExit   = 1003
	idTrayStatus = 1004
)

const (
	eventSystemForeground  = 0x0003
	eventSystemMinimizeEnd = 0x0017
	winEventOutOfContext   = 0x0000
	winEventSkipOwnProcess = 0x0002
)

const (
	pbtAPMSuspend         = 0x0004
	pbtAPMResumeSuspend   = 0x0007
	pbtAPMResumeAutomatic = 0x0012
)

const (
	nimAdd    = 0x00000000
	nimModify = 0x00000001
	nimDelete = 0x00000002

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100

	mfString    = 0x0000
	mfSeparator = 0x0800
	mfChecked   = 0x0008
	mfUnchecked = 0x0000

	idiApplication = 32512

	imageIcon       = 1
	lrLoadFromFile  = 0x0010
	smCXSmIcon      = 49
	smCYSmIcon      = 50
	hkeyCurrentUser = 0x80000001
	keyRead         = 0x20019
	regDword        = 4
	grGdiObjects    = 0
	grUserObjects   = 1
)

type point struct {
	x int32
	y int32
}

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

type notifyIconData struct {
	cbSize           uint32
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            uintptr
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         guid
	hBalloonIcon     uintptr
}

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type msgStruct struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

type lastInputInfo struct {
	cbSize uint32
	dwTime uint32
}

type filetime struct {
	low  uint32
	high uint32
}

func u16Ptr(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return nil
	}
	return p
}

func windowString(hwnd uintptr, proc *syscall.LazyProc, size int) string {
	if hwnd == 0 {
		return ""
	}
	buf := make([]uint16, size)
	n, _, _ := proc.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:int(n)])
}

func processCPUSeconds() float64 {
	var creation, exit, kernel, user filetime
	cur, _, _ := procGetCurrentProcess.Call()
	ok, _, _ := procGetProcessTimes.Call(cur,
		uintptr(unsafe.Pointer(&creation)), uintptr(unsafe.Pointer(&exit)),
		uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
	if ok == 0 {
		return -1
	}
	toSec := func(ft filetime) float64 {
		return float64(uint64(ft.high)<<32|uint64(ft.low)) / 1e7
	}
	return toSec(kernel) + toSec(user)
}
