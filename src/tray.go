package main

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

var appInstance *App

// wndProcCallback 必须保持包级函数，回调签名只能使用 uintptr 大小类型。
func wndProcCallback(hwnd, msg, wParam, lParam uintptr) uintptr {
	defer func() {
		if r := recover(); r != nil && appInstance != nil {
			appInstance.store.Log("PANIC in wndProc callback (swallowed): " + toStr(r))
		}
	}()
	if appInstance == nil {
		return 0
	}
	return appInstance.wndProc(hwnd, msg, wParam, lParam)
}

func winEventCallback(hook, event, hwnd, idObject, idChild, thread, ms uintptr) uintptr {
	defer func() {
		if r := recover(); r != nil && appInstance != nil {
			appInstance.store.Log("PANIC in winEvent callback (swallowed): " + toStr(r))
		}
	}()
	if appInstance == nil {
		return 0
	}
	if uint32(event) == eventSystemForeground || uint32(event) == eventSystemMinimizeEnd {
		appInstance.onForeground()
	}
	return 0
}

func toStr(r any) string {
	if s, ok := r.(string); ok {
		return s
	}
	if e, ok := r.(error); ok {
		return e.Error()
	}
	return "non-string panic"
}

func (a *App) wndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	// explorer 重启后，任务栏会广播 TaskbarCreated；我们必须重新注册图标，否则图标永久消失
	if a.taskbarMsg != 0 && uint32(msg) == a.taskbarMsg {
		a.store.Log("TaskbarCreated received: re-adding tray icon")
		a.trayOK = false
		a.nid = nil
		if err := a.addTray(); err != nil {
			a.store.Log("tray re-add failed: " + err.Error())
		}
		return 0
	}
	switch uint32(msg) {
	case wmTimer:
		a.onTick()
		return 0
	case wmTrayIcon:
		if lParam == wmLButtonUp || lParam == wmRButtonUp || lParam == wmLButtonDblClk {
			a.showMenu()
		}
		return 0
	case wmWiFiEvent:
		a.onWiFiEvent(uint32(wParam))
		return 0
	case wmPowerBroadcast:
		switch uint32(wParam) {
		case pbtAPMSuspend:
			a.onSuspend()
		case pbtAPMResumeSuspend, pbtAPMResumeAutomatic:
			a.onResume()
		}
		return 1
	case wmQueryEndSession:
		return 1
	case wmEndSession:
		if wParam != 0 {
			a.shutdown("session_end_confirmed")
		}
		return 0
	case wmClose:
		a.shutdown("wm_close")
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wParam, lParam)
	return r
}

func (a *App) onTick() {
	defer func() {
		if r := recover(); r != nil {
			a.store.Log("PANIC in onTick (swallowed): " + toStr(r))
		}
	}()
	a.tick()
	if time.Since(a.lastStatusMono).Seconds() >= 10 {
		a.writeStatus()
		a.pollDrops()
		a.refreshTrayTip()
		a.refreshTrayIcon()
		a.retryTrayIfNeeded()
	}
	if time.Since(a.lastReconcileMono).Seconds() >= a.cfg.ReconcileSec {
		a.reconcile()
	}
}

// ---------------- 窗口与托盘 ----------------

func (a *App) createWindow() error {
	hInst, _, _ := procGetModuleHandleW.Call(0)
	className := "ScreenTimeObserverM1"
	wc := wndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		lpfnWndProc:   a.wndProcCB,
		hInstance:     hInst,
		lpszClassName: u16Ptr(className),
	}
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	cname := u16Ptr(className)
	title := u16Ptr(collectorName)
	hwnd, _, err := procCreateWindowExW.Call(0,
		uintptr(unsafe.Pointer(cname)), uintptr(unsafe.Pointer(title)),
		0, 0, 0, 0, 0, 0, 0, hInst, 0)
	runtime.KeepAlive(cname)
	runtime.KeepAlive(title)
	if hwnd == 0 {
		return err
	}
	a.hwnd = hwnd
	return nil
}

func setTip(nid *notifyIconData, s string) {
	setWideText(nid.szTip[:], s)
}

func setWideText(dst []uint16, s string) {
	u := syscall.StringToUTF16(s)
	if len(u) > len(dst) {
		u = u[:len(dst)]
		u[len(u)-1] = 0
	}
	for i := range dst {
		dst[i] = 0
	}
	copy(dst, u)
}

func (a *App) addTray() error {
	nid := &notifyIconData{
		cbSize:           uint32(unsafe.Sizeof(notifyIconData{})),
		hWnd:             a.hwnd,
		uID:              1,
		uFlags:           nifIcon | nifMessage | nifTip,
		uCallbackMessage: wmTrayIcon,
	}
	// 复用已加载的图标句柄，避免重试时反复 LoadImage 泄漏句柄
	if a.trayIconH == 0 {
		a.trayIconH = a.loadTrayIcon()
	}
	nid.hIcon = a.trayIconH
	setTip(nid, collectorName)
	ok, _, err := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(nid)))
	runtime.KeepAlive(nid)
	if ok == 0 {
		return err
	}
	a.nid = nid
	a.trayOK = true
	return nil
}

func (a *App) removeTray() {
	if a.trayOK && a.nid != nil {
		procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(a.nid)))
		a.trayOK = false
	}
}

func (a *App) refreshTrayTip() {
	if !a.trayOK || a.nid == nil {
		return
	}
	tip := collectorName + " · " + a.stateText()
	if a.alerting() {
		tip += " · " + a.alertDetail()
	} else if !a.paused && !a.suspended && a.fg.Process != "" {
		tip += " · " + a.fg.Process
	}
	setTip(a.nid, tip)
	a.nid.uFlags = nifIcon | nifMessage | nifTip
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(a.nid)))
}

func (a *App) showMenu() {
	menu, _, _ := procCreatePopupMenu.Call()
	defer procDestroyMenu.Call(menu)
	dashboardLabel := u16Ptr("打开仪表盘")
	procAppendMenuW.Call(menu, mfString, idTrayDashboard, uintptr(unsafe.Pointer(dashboardLabel)))
	runtime.KeepAlive(dashboardLabel)
	procAppendMenuW.Call(menu, mfSeparator, 0, 0)

	pauseLabel := u16Ptr("暂停采集")
	pauseFlags := uintptr(mfString | mfUnchecked)
	if a.paused {
		pauseLabel = u16Ptr("继续采集")
		pauseFlags = mfString | mfChecked
	}
	procAppendMenuW.Call(menu, pauseFlags, idTrayPause, uintptr(unsafe.Pointer(pauseLabel)))
	runtime.KeepAlive(pauseLabel)
	procAppendMenuW.Call(menu, mfSeparator, 0, 0)

	statusLabel := u16Ptr("状态：" + a.stateText())
	procAppendMenuW.Call(menu, mfString, idTrayStatus, uintptr(unsafe.Pointer(statusLabel)))
	runtime.KeepAlive(statusLabel)
	wifiStatusLabel := u16Ptr("Wi-Fi 位置：" + a.wifiStateText())
	procAppendMenuW.Call(menu, mfString, idTrayWiFiStatus, uintptr(unsafe.Pointer(wifiStatusLabel)))
	runtime.KeepAlive(wifiStatusLabel)
	wifiToggleText := "启用 Wi-Fi 位置指纹…"
	if a.wifiEnabled.Load() {
		wifiToggleText = "停用 Wi-Fi 位置指纹"
	}
	wifiToggleLabel := u16Ptr(wifiToggleText)
	procAppendMenuW.Call(menu, mfString, idTrayWiFiToggle, uintptr(unsafe.Pointer(wifiToggleLabel)))
	runtime.KeepAlive(wifiToggleLabel)
	locationLabel := u16Ptr("打开 Windows 位置权限设置")
	procAppendMenuW.Call(menu, mfString, idTrayLocationSettings, uintptr(unsafe.Pointer(locationLabel)))
	runtime.KeepAlive(locationLabel)
	procAppendMenuW.Call(menu, mfSeparator, 0, 0)

	dataLabel := u16Ptr("打开数据目录")
	procAppendMenuW.Call(menu, mfString, idTrayOpen, uintptr(unsafe.Pointer(dataLabel)))
	runtime.KeepAlive(dataLabel)
	copyLabel := u16Ptr("复制数据目录")
	procAppendMenuW.Call(menu, mfString, idTrayCopyPath, uintptr(unsafe.Pointer(copyLabel)))
	runtime.KeepAlive(copyLabel)
	procAppendMenuW.Call(menu, mfSeparator, 0, 0)

	exitLabel := u16Ptr("退出采集器")
	procAppendMenuW.Call(menu, mfString, idTrayExit, uintptr(unsafe.Pointer(exitLabel)))
	runtime.KeepAlive(exitLabel)

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(a.hwnd)
	cmd, _, _ := procTrackPopupMenu.Call(menu,
		tpmRightButton|tpmReturnCmd, uintptr(pt.x), uintptr(pt.y), 0, a.hwnd, 0)
	procPostMessageW.Call(a.hwnd, wmNull, 0, 0)

	switch int32(cmd) {
	case idTrayDashboard:
		a.openDashboard()
	case idTrayPause:
		a.togglePause()
	case idTrayOpen:
		a.openDataDir()
	case idTrayCopyPath:
		a.copyDataDir()
	case idTrayWiFiToggle:
		a.toggleWiFi()
	case idTrayLocationSettings:
		a.openLocationSettings()
	case idTrayExit:
		a.shutdown("tray_exit")
	}
}

// ---------------- 启动与消息循环 ----------------

func (a *App) installHooks() {
	a.winEventCB = syscall.NewCallback(winEventCallback)
	h1, _, _ := procSetWinEventHook.Call(eventSystemForeground, eventSystemForeground, 0,
		a.winEventCB, 0, 0, winEventOutOfContext|winEventSkipOwnProcess)
	h2, _, _ := procSetWinEventHook.Call(eventSystemMinimizeEnd, eventSystemMinimizeEnd, 0,
		a.winEventCB, 0, 0, winEventOutOfContext|winEventSkipOwnProcess)
	a.hookFG, a.hookMin = h1, h2
	a.hookOK = h1 != 0 && h2 != 0
	if !a.hookOK {
		a.store.Log("SetWinEventHook failed: foreground=" + itoa(int64(h1)) + " minimize=" + itoa(int64(h2)))
	}
}

// retryTrayIfNeeded 处理两类真实故障：登录时 explorer 尚未就绪、图标被系统清掉。
func (a *App) retryTrayIfNeeded() {
	if a.headless || a.hwnd == 0 || a.trayOK {
		return
	}
	if time.Since(a.lastTrayTry).Seconds() < 3 {
		return
	}
	a.lastTrayTry = time.Now()
	a.trayAttempts++
	if err := a.addTray(); err != nil {
		a.store.Log("tray add attempt " + itoa(int64(a.trayAttempts)) + " failed: " + err.Error())
		return
	}
	a.store.Log("tray icon added after " + itoa(int64(a.trayAttempts)) + " attempts")
}

func (a *App) run(maxSeconds float64) int {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	enablePerMonitorDPI()

	store, err := newStore(a.cfg.DataDir, a.cfg.LogDir)
	if err != nil {
		os.Stderr.WriteString("cannot prepare data/log dir: " + err.Error() + newline)
		return 2
	}
	a.store = store
	// COM 要在线程上初始化；必须等日志存储就绪后再记录初始化失败。
	if err := comInit(); err != nil {
		a.store.Log(err.Error() + "，音频检测不可用")
	}
	if ep := lastRecordEpoch(a.cfg.DataDir); ep > 0 {
		a.lastEpoch = ep
	}
	a.startedMono = time.Now()
	a.startedAt = time.Now()
	a.carryDay = time.Now().Format("2006-01-02")

	a.paused = fileExists(a.pausePath)
	a.screenshotPaused.Store(a.paused)
	a.wifiPaused.Store(a.paused)
	if a.paused {
		a.store.Log("start in paused state (pause flag present)")
	}

	msgName := u16Ptr("TaskbarCreated")
	if id, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(msgName))); id != 0 {
		a.taskbarMsg = uint32(id)
	}
	runtime.KeepAlive(msgName)
	a.wndProcCB = syscall.NewCallback(wndProcCallback)
	a.installHooks()

	if !a.headless {
		if err := a.createWindow(); err != nil {
			a.store.Log("window creation failed: " + err.Error())
		} else if err := a.addTray(); err != nil {
			a.store.Log("TRAY ADD FAILED (采集继续，托盘不可用): " + err.Error())
		}
		procSetTimer.Call(a.hwnd, timerTickID, uintptr(int(a.cfg.TickSec*1000)), 0)
	} else {
		a.store.Log("headless mode: no tray, no window timer")
	}
	a.initWiFiIfEnabled()

	a.refreshForeground()
	a.refreshIdle()
	a.emitState("start", "process_start")
	a.writeStatus()
	a.maybeMaintainScreenshots()
	a.maybeMaintainWiFi()
	a.startDashboard()
	a.store.Log("collector start pid=" + itoa(int64(os.Getpid())) +
		" idle_threshold=" + ftoa(a.cfg.IdleThresholdSec) + "s hook_ok=" + btoa(a.hookOK) +
		" tray=" + btoa(a.trayOK) + " data_dir=" + a.cfg.DataDir)

	if a.hwnd != 0 {
		a.messageLoop(maxSeconds)
	} else {
		a.headlessLoop(maxSeconds)
	}
	if !a.quitFlag {
		a.shutdown("loop_exit")
	}
	return 0
}

func (a *App) messageLoop(maxSeconds float64) {
	var m msgStruct
	for !a.quitFlag {
		if maxSeconds > 0 && time.Since(a.startedMono).Seconds() >= maxSeconds {
			a.shutdown("max_seconds")
			return
		}
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// headlessLoop 无窗口时自己驱动节拍，同时非阻塞排空消息队列让钩子回调得以执行。
func (a *App) headlessLoop(maxSeconds float64) {
	var m msgStruct
	last := time.Now()
	for !a.quitFlag {
		if maxSeconds > 0 && time.Since(a.startedMono).Seconds() >= maxSeconds {
			a.shutdown("max_seconds")
			return
		}
		for {
			has, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, 1)
			if has == 0 {
				break
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
		if time.Since(last).Seconds() >= a.cfg.TickSec {
			last = time.Now()
			a.onTick()
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// ---------------- 图标与任务栏主题 ----------------

// systemUsesLightTheme 读系统/任务栏主题：true 为浅色（配深色图标），false 为深色（配白色单色图标）。
// 第二个返回值表示是否读取成功；读不到时调用方按浅色处理，绝不静默假定。
func systemUsesLightTheme() (bool, bool) {
	sub := u16Ptr(`SOFTWARE\Microsoft\Windows\CurrentVersion\Themes\Personalize`)
	name := u16Ptr(`SystemUsesLightTheme`)
	var hkey uintptr
	r, _, _ := procRegOpenKeyExW.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(sub)), 0,
		keyRead, uintptr(unsafe.Pointer(&hkey)))
	runtime.KeepAlive(sub)
	if r != 0 || hkey == 0 {
		return false, false
	}
	defer procRegCloseKey.Call(hkey)
	var typ, data, size uint32 = 0, 0, 4
	r2, _, _ := procRegQueryValueExW.Call(hkey, uintptr(unsafe.Pointer(name)), 0,
		uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&size)))
	runtime.KeepAlive(name)
	if r2 != 0 || typ != regDword {
		return false, false
	}
	return data != 0, true
}

func (a *App) loadIconFile(path string) uintptr {
	if !fileExists(path) {
		a.store.Log("icon file missing: " + path)
		return 0
	}
	p := u16Ptr(path)
	cx, _, _ := procGetSystemMetrics.Call(smCXSmIcon)
	cy, _, _ := procGetSystemMetrics.Call(smCYSmIcon)
	if cx == 0 {
		cx = 16
	}
	if cy == 0 {
		cy = 16
	}
	h, _, _ := procLoadImageW.Call(0, uintptr(unsafe.Pointer(p)), imageIcon, cx, cy, lrLoadFromFile)
	runtime.KeepAlive(p)
	if h == 0 {
		a.store.Log("LoadImageW failed for " + path)
	}
	return h
}

// stateIconPath 由图标目录推出状态变体路径；文件不存在时返回空串，交给调用方回退。
func (a *App) stateIconPath(ink, state string) string {
	if a.cfg.IconStateDir == "" {
		return ""
	}
	p := filepath.Join(a.cfg.IconStateDir, ink+"-"+state+".ico")
	if fileExists(p) {
		return p
	}
	return ""
}

func inkName(light bool) string {
	if light {
		return "light"
	}
	return "dark"
}

// pickIcon 按主题与状态挑图标，逐级回退，每次回退都留日志，不静默换图。
func (a *App) pickIcon(wantLight bool, state string) (uintptr, bool) {
	ink, base, other := "dark", a.cfg.IconMonoPath, a.cfg.IconPath
	if wantLight {
		ink, base, other = "light", a.cfg.IconPath, a.cfg.IconMonoPath
	}
	if p := a.stateIconPath(ink, state); p != "" {
		if h := a.loadIconFile(p); h != 0 {
			return h, true
		}
	} else {
		a.store.Log("state icon missing: " + ink + "-" + state + ".ico，回退到基础图标")
	}
	if h := a.loadIconFile(base); h != 0 {
		return h, true
	}
	if h := a.loadIconFile(other); h != 0 {
		a.store.Log("基础图标缺失，改用另一套主题图标: " + other)
		return h, true
	}
	a.store.Log("ICON FALLBACK: 没有可用图标文件，使用系统默认图标")
	sys, _, _ := procLoadIconW.Call(0, idiApplication)
	return sys, false
}

// loadTrayIcon 启动时按主题与当前状态挑图标。
func (a *App) loadTrayIcon() uintptr {
	light, known := systemUsesLightTheme()
	want := !known || light
	state := a.stateKey()
	h, owned := a.pickIcon(want, state)
	a.trayIconLight = want
	a.trayIconState = state
	a.trayIconOwned = owned
	if h != 0 {
		a.trayIconH = h
	}
	if !known {
		a.store.Log("theme unknown, using light-theme icon")
	}
	return h
}

// refreshTrayIcon 主题或状态变化后热替换图标，并在异常与恢复时各弹一次气泡。
func (a *App) refreshTrayIcon() {
	if !a.trayOK || a.nid == nil {
		return
	}
	light, known := systemUsesLightTheme()
	want := !known || light
	state := a.stateKey()
	if want == a.trayIconLight && state == a.trayIconState {
		return
	}
	prev := a.trayIconState
	h, owned := a.pickIcon(want, state)
	if h == 0 {
		a.store.Log("theme/state switch: 没有可用图标，保持当前图标")
		return
	}
	if a.trayIconOwned && a.trayIconH != 0 && a.trayIconH != h {
		procDestroyIcon.Call(a.trayIconH)
	}
	a.trayIconH = h
	a.trayIconOwned = owned
	a.trayIconLight = want
	a.trayIconState = state
	a.nid.hIcon = h
	a.nid.uFlags = nifIcon | nifMessage | nifTip
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(a.nid)))
	a.store.Log("tray icon switched: state=" + state + " ink=" + inkName(want))
	if prev == "" || prev == state {
		return
	}
	if state == "alert" {
		a.notify("采集异常", a.alertDetail())
	} else if prev == "alert" {
		a.notify("采集已恢复", "当前状态："+a.stateText())
	}
}

func btoa(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// copyDataDir 把数据目录路径放进剪贴板，方便直接粘给别的程序或 AI。
func (a *App) copyDataDir() {
	dir, err := filepath.Abs(filepath.FromSlash(a.cfg.DataDir))
	if err != nil {
		a.store.Log("cannot resolve data dir: " + err.Error())
		return
	}
	if err := setClipboardText(dir); err != nil {
		a.store.Log("copy data dir failed: " + err.Error())
		a.notify("复制失败", "剪贴板被其他程序占用，稍后再试")
		return
	}
	a.store.Log("copied data dir to clipboard: " + dir)
	a.notify("已复制数据目录", dir)
}

// notify 用托盘气泡给一次操作反馈；系统关闭通知时只是不显示，不影响功能。
func (a *App) notify(title, text string) {
	if !a.trayOK || a.nid == nil {
		return
	}
	setWideText(a.nid.szInfoTitle[:], title)
	setWideText(a.nid.szInfo[:], text)
	a.nid.dwInfoFlags = niifInfo
	a.nid.uFlags = nifIcon | nifMessage | nifTip | nifInfo
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(a.nid)))
	a.nid.uFlags = nifIcon | nifMessage | nifTip
}
