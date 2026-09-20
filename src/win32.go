package main

import (
	"errors"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

// 全部通过纯标准库绑定，不引入任何第三方依赖。

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
)

var (
	procGetForegroundWindow           = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessID      = user32.NewProc("GetWindowThreadProcessId")
	procGetWindowTextW                = user32.NewProc("GetWindowTextW")
	procGetClassNameW                 = user32.NewProc("GetClassNameW")
	procGetLastInputInfo              = user32.NewProc("GetLastInputInfo")
	procOpenInputDesktop              = user32.NewProc("OpenInputDesktop")
	procCloseDesktop                  = user32.NewProc("CloseDesktop")
	procSetWinEventHook               = user32.NewProc("SetWinEventHook")
	procUnhookWinEvent                = user32.NewProc("UnhookWinEvent")
	procRegisterClassExW              = user32.NewProc("RegisterClassExW")
	procCreateWindowExW               = user32.NewProc("CreateWindowExW")
	procDefWindowProcW                = user32.NewProc("DefWindowProcW")
	procDestroyWindow                 = user32.NewProc("DestroyWindow")
	procSetTimer                      = user32.NewProc("SetTimer")
	procKillTimer                     = user32.NewProc("KillTimer")
	procLoadIconW                     = user32.NewProc("LoadIconW")
	procCreatePopupMenu               = user32.NewProc("CreatePopupMenu")
	procAppendMenuW                   = user32.NewProc("AppendMenuW")
	procDestroyMenu                   = user32.NewProc("DestroyMenu")
	procTrackPopupMenu                = user32.NewProc("TrackPopupMenu")
	procGetCursorPos                  = user32.NewProc("GetCursorPos")
	procGetMessageW                   = user32.NewProc("GetMessageW")
	procPeekMessageW                  = user32.NewProc("PeekMessageW")
	procTranslateMessage              = user32.NewProc("TranslateMessage")
	procDispatchMessageW              = user32.NewProc("DispatchMessageW")
	procPostQuitMessage               = user32.NewProc("PostQuitMessage")
	procPostMessageW                  = user32.NewProc("PostMessageW")
	procSetForegroundWindow           = user32.NewProc("SetForegroundWindow")
	procShellNotifyIconW              = shell32.NewProc("Shell_NotifyIconW")
	procOpenProcess                   = kernel32.NewProc("OpenProcess")
	procQueryFullProcessImageNameW    = kernel32.NewProc("QueryFullProcessImageNameW")
	procCloseHandle                   = kernel32.NewProc("CloseHandle")
	procCreateMutexW                  = kernel32.NewProc("CreateMutexW")
	procGetModuleHandleW              = kernel32.NewProc("GetModuleHandleW")
	procGetCurrentProcess             = kernel32.NewProc("GetCurrentProcess")
	procGetProcessTimes               = kernel32.NewProc("GetProcessTimes")
	procGetTickCount                  = kernel32.NewProc("GetTickCount")
	procGetProcessHandleCount         = kernel32.NewProc("GetProcessHandleCount")
	procGetTickCount64                = kernel32.NewProc("GetTickCount64")
	procGetGuiResources               = user32.NewProc("GetGuiResources")
	procRegisterWindowMessageW        = user32.NewProc("RegisterWindowMessageW")
	procLoadImageW                    = user32.NewProc("LoadImageW")
	procGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	procDestroyIcon                   = user32.NewProc("DestroyIcon")
	procGetDC                         = user32.NewProc("GetDC")
	procReleaseDC                     = user32.NewProc("ReleaseDC")
	procGetCursorInfo                 = user32.NewProc("GetCursorInfo")
	procGetIconInfo                   = user32.NewProc("GetIconInfo")
	procDrawIconEx                    = user32.NewProc("DrawIconEx")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware            = user32.NewProc("SetProcessDPIAware")
	procCreateCompatibleDC            = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC                      = gdi32.NewProc("DeleteDC")
	procCreateCompatibleBitmap        = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject                  = gdi32.NewProc("SelectObject")
	procBitBlt                        = gdi32.NewProc("BitBlt")
	procGetDIBits                     = gdi32.NewProc("GetDIBits")
	procDeleteObject                  = gdi32.NewProc("DeleteObject")
	procRegOpenKeyExW                 = advapi32.NewProc("RegOpenKeyExW")
	procRegQueryValueExW              = advapi32.NewProc("RegQueryValueExW")
	procRegCloseKey                   = advapi32.NewProc("RegCloseKey")
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
	wmWiFiEvent = wmApp + 2
	timerTickID = 1

	idTrayPause            = 1001
	idTrayOpen             = 1002
	idTrayExit             = 1003
	idTrayStatus           = 1004
	idTrayCopyPath         = 1005
	idTrayWiFiToggle       = 1006
	idTrayWiFiStatus       = 1007
	idTrayLocationSettings = 1008
	idTrayDashboard        = 1009
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

var (
	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procSetClipboardData = user32.NewProc("SetClipboardData")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
	procGlobalLock       = kernel32.NewProc("GlobalLock")
	procGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
	procGlobalFree       = kernel32.NewProc("GlobalFree")
	procRtlMoveMemory    = kernel32.NewProc("RtlMoveMemory")
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002

	nifInfo  = 0x00000010
	niifInfo = 0x00000001
)

// setClipboardText 把文本放进剪贴板。剪贴板可能被别的程序短暂占用，所以重试几次。
// 内存用 RtlMoveMemory 直接写，避免 uintptr 与指针互转带来的不安全性。
func setClipboardText(s string) error {
	u, err := syscall.UTF16FromString(s)
	if err != nil {
		return err
	}
	size := uintptr(len(u) * 2)
	h, _, allocErr := procGlobalAlloc.Call(gmemMoveable, size)
	if h == 0 {
		return allocErr
	}
	ptr, _, lockErr := procGlobalLock.Call(h)
	if ptr == 0 {
		procGlobalFree.Call(h)
		return lockErr
	}
	procRtlMoveMemory.Call(ptr, uintptr(unsafe.Pointer(&u[0])), size)
	runtime.KeepAlive(u)
	procGlobalUnlock.Call(h)

	var last error
	for attempt := 0; attempt < 5; attempt++ {
		ok, _, openErr := procOpenClipboard.Call(0)
		if ok == 0 {
			last = openErr
			time.Sleep(60 * time.Millisecond)
			continue
		}
		procEmptyClipboard.Call()
		set, _, setErr := procSetClipboardData.Call(cfUnicodeText, h)
		procCloseClipboard.Call()
		if set != 0 {
			return nil // 内存所有权已交给剪贴板，这里不能再释放
		}
		procGlobalFree.Call(h)
		return setErr
	}
	procGlobalFree.Call(h)
	if last == nil {
		last = errors.New("打开剪贴板失败")
	}
	return last
}

var (
	ole32 = syscall.NewLazyDLL("ole32.dll")

	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
)

const (
	coinitApartmentThreaded = 0x0002
	clsctxAll               = 0x0017
	rpcChangedMode          = 0x80010106
)

// 音频接口的 CLSID / IID：默认播放设备枚举器与音量监听接口。
var (
	clsidMMDeviceEnumerator   = guid{Data1: 0xBCDE0395, Data2: 0xE52F, Data3: 0x467C, Data4: [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E}}
	iidIMMDeviceEnumerator    = guid{Data1: 0xA95664D2, Data2: 0x9614, Data3: 0x4F35, Data4: [8]byte{0xA7, 0x46, 0xDE, 0x8D, 0xB6, 0x36, 0x17, 0xE6}}
	iidIAudioMeterInformation = guid{Data1: 0xC02216F6, Data2: 0x8C67, Data3: 0x4B5B, Data4: [8]byte{0x9D, 0x00, 0xD0, 0x08, 0xE7, 0x3E, 0x00, 0x64}}
)
