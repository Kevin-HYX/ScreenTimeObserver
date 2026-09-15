package main

import (
	"encoding/json"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const collectorName = "屏幕时间采集器"
const mutexName = "ScreenTimeObserver.M1.Collector"

type Config struct {
	DataDir            string  `json:"data_dir"`
	LogDir             string  `json:"log_dir"`
	IdleThresholdSec   float64 `json:"idle_threshold_sec"`
	TickSec            float64 `json:"tick_sec"`
	ReconcileSec       float64 `json:"reconcile_sec"`
	HeartbeatSec       float64 `json:"heartbeat_sec"`
	PausedHeartbeatSec float64 `json:"paused_heartbeat_sec"`
	GapReportSec       float64 `json:"gap_report_sec"`
	TitleMaxLen        int     `json:"title_max_len"`
	Tray               bool    `json:"tray"`
	IconPath           string  `json:"icon_path"`
	IconMonoPath       string  `json:"icon_mono_path"`
}

func defaultConfig(exeDir string) Config {
	return Config{
		DataDir:            filepath.Join(exeDir, "data"),
		LogDir:             filepath.Join(exeDir, "logs"),
		IdleThresholdSec:   180,
		TickSec:            1,
		ReconcileSec:       30,
		HeartbeatSec:       60,
		PausedHeartbeatSec: 60,
		GapReportSec:       90,
		TitleMaxLen:        300,
		Tray:               true,
		IconPath:           filepath.Join(exeDir, "assets", "logo.ico"),
		IconMonoPath:       filepath.Join(exeDir, "assets", "logo-mono.ico"),
	}
}

// loadConfig 以默认值为底，用 config.json 覆盖（缺项保持默认）。
func loadConfig(exeDir, path string) (Config, error) {
	cfg := defaultConfig(exeDir)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, err
	}
	// 路径归一化：无论配置里写成 / 还是 \，下游统一拿到系统原生分隔符
	for _, p := range []*string{&cfg.DataDir, &cfg.LogDir, &cfg.IconPath, &cfg.IconMonoPath} {
		*p = filepath.FromSlash(*p)
		if abs, err := filepath.Abs(*p); err == nil {
			*p = abs
		}
	}
	return cfg, nil
}

// App 持有全部运行状态。主线程跑消息循环，写盘在 Store 的独立 goroutine 里。
type App struct {
	cfg       Config
	exeDir    string
	store     *Store
	pausePath string

	startedMono time.Time
	startedAt   time.Time

	fg        Sample
	idleSec   float64
	idle      bool
	locked    bool
	suspended bool
	paused    bool

	lastKey           string
	lastEpoch         float64
	lastWriteMono     time.Time
	lastReconcileMono time.Time
	lastPausedWrite   time.Time
	lastStatusMono    time.Time
	carryDay          string

	tickCount int64
	fgEvents  int64

	hwnd          uintptr
	trayIconH     uintptr
	trayIconOwned bool
	trayIconLight bool
	taskbarMsg    uint32
	trayAttempts  int
	lastTrayTry   time.Time
	nid           *notifyIconData
	trayOK        bool
	hookFG        uintptr
	hookMin       uintptr
	hookOK        bool
	headless      bool
	wndProcCB     uintptr
	winEventCB    uintptr

	quitFlag   bool
	exitReason string
	verbose    bool
}

func newApp(cfg Config, exeDir string, headless, verbose bool) *App {
	return &App{
		cfg:       cfg,
		exeDir:    exeDir,
		pausePath: filepath.Join(exeDir, "paused.flag"),
		headless:  headless,
		verbose:   verbose,
	}
}

// ---------------- 记录构造 ----------------

func clip(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

func (a *App) session() string {
	switch {
	case a.suspended:
		return "suspended"
	case a.locked:
		return "locked"
	default:
		return "active"
	}
}

func (a *App) newRecord(event, reason string) Record {
	now := time.Now()
	rec := Record{
		TS:     now.Format("2006-01-02T15:04:05.000-07:00"),
		Epoch:  math.Round(float64(now.UnixNano())/1e6) / 1000,
		Event:  event,
		Reason: reason,
	}
	if a.lastEpoch > 0 {
		d := math.Round((rec.Epoch-a.lastEpoch)*1000) / 1000
		rec.SincePrev = &d
		rec.Gap = d > a.cfg.GapReportSec
	}
	return rec
}

func (a *App) stateRecord(event, reason string) Record {
	rec := a.newRecord(event, reason)
	if a.paused {
		rec.Session = "paused"
		rec.Paused = true
		return rec
	}
	rec.Session = a.session()
	rec.Process = a.fg.Process
	rec.PID = a.fg.PID
	rec.Title = clip(a.fg.Title, a.cfg.TitleMaxLen)
	rec.WindowClass = a.fg.WindowClass
	rec.IdleSec = nanSeconds(a.idleSec)
	rec.Idle = a.idle
	rec.Locked = a.locked
	rec.Paused = false
	return rec
}

// pauseRecord 只有暂停标志，不含任何窗口细节。
func (a *App) pauseRecord(event string) Record {
	rec := a.newRecord(event, event)
	rec.Session = "paused"
	rec.Paused = true
	return rec
}

func (a *App) emit(rec Record) {
	a.lastEpoch = rec.Epoch
	a.store.Enqueue(rec)
	if a.verbose {
		if b, err := json.Marshal(rec); err == nil {
			os.Stdout.Write(append(b, newline...))
		}
	}
}

func (a *App) emitState(event, reason string) {
	a.emit(a.stateRecord(event, reason))
	a.lastKey = a.key()
	a.lastWriteMono = time.Now()
}

func (a *App) key() string {
	return a.session() + "|" + itoa(boolToInt(a.idle)) + "|" + a.fg.Process + "|" +
		itoa(int64(a.fg.PID)) + "|" + a.fg.Title + "|" + itoa(boolToInt(a.paused))
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// ---------------- 采样驱动 ----------------

func (a *App) refreshIdle() bool {
	if a.paused {
		a.idleSec = math.NaN()
		a.idle = false
		return false
	}
	was := a.idle
	a.idleSec = idleSeconds()
	a.idle = !math.IsNaN(a.idleSec) && a.idleSec >= a.cfg.IdleThresholdSec
	return was != a.idle
}

func (a *App) refreshForeground() {
	if a.paused {
		a.fg = Sample{}
		return
	}
	a.fg = sampleForeground()
	a.locked = sessionLocked()
}

func (a *App) syncPause() {
	want := fileExists(a.pausePath)
	if want == a.paused {
		return
	}
	a.paused = want
	if a.paused {
		a.fg = Sample{}
		a.idleSec = math.NaN()
		a.idle = false
		a.emit(a.pauseRecord("paused"))
		a.lastPausedWrite = time.Now()
		a.store.Log("paused by user (flag file present)")
	} else {
		a.refreshForeground()
		a.refreshIdle()
		a.emitState("resumed", "user_resume")
		a.store.Log("resumed by user")
	}
	a.writeStatus()
	a.refreshTrayTip()
}

func (a *App) tick() {
	a.tickCount++
	a.syncPause()

	if a.suspended {
		return
	}
	a.maybeCarryOver()

	if a.paused {
		a.idleSec = math.NaN()
		if time.Since(a.lastPausedWrite).Seconds() >= a.cfg.PausedHeartbeatSec {
			a.emit(a.pauseRecord("paused"))
			a.lastPausedWrite = time.Now()
		}
		return
	}

	idleChanged := a.refreshIdle()
	if idleChanged {
		a.emitState("change", "idle_threshold")
		return
	}
	if time.Since(a.lastWriteMono).Seconds() >= a.cfg.HeartbeatSec {
		a.emitState("heartbeat", "heartbeat")
	}
}

// maybeCarryOver 在新的一天写入承接记录，明确延续上一天最后的状态。
func (a *App) maybeCarryOver() {
	day := time.Now().Format("2006-01-02")
	if a.carryDay == day {
		return
	}
	a.carryDay = day
	if a.lastEpoch == 0 {
		return
	}
	a.emitState("carry_over", "new_day")
}

// onForeground 前台窗口变化（事件驱动主通道）。
func (a *App) onForeground() {
	if a.suspended || a.paused {
		return
	}
	a.fgEvents++
	a.refreshIdle()
	a.refreshForeground()
	if a.key() == a.lastKey {
		// 同一次切换可能成对到达 FOREGROUND 与 MINIMIZEEND 两个事件，状态没变就不写重复记录
		return
	}
	a.emitState("change", "foreground")
}

// reconcile 低频对账：兜住漏掉的事件，并刷新状态文件与托盘提示。
func (a *App) reconcile() {
	a.lastReconcileMono = time.Now()
	if !a.suspended && !a.paused {
		a.refreshForeground()
		a.refreshIdle()
		if a.key() != a.lastKey {
			a.emitState("change", "reconcile")
			a.store.Log("reconcile fixed a missed state change")
		}
	}
	a.writeStatus()
	a.refreshTrayTip()
}

func (a *App) writeStatus() {
	idle := a.cfg.IdleThresholdSec
	st := map[string]any{
		"process":            "collector",
		"pid":                os.Getpid(),
		"updated":            time.Now().Format("2006-01-02T15:04:05-07:00"),
		"started_at":         a.startedAt.Format("2006-01-02T15:04:05-07:00"),
		"uptime_sec":         int(time.Since(a.startedMono).Seconds()),
		"ticks":              a.tickCount,
		"foreground_events":  a.fgEvents,
		"records_written":    a.store.Written(),
		"records_dropped":    a.store.Dropped(),
		"paused":             a.paused,
		"session":            a.session(),
		"idle":               a.idle,
		"idle_sec":           nanSeconds(a.idleSec),
		"idle_threshold_sec": idle,
		"foreground_process": a.fg.Process,
		"foreground_title":   clip(a.fg.Title, 120),
		"hook_ok":            a.hookOK,
		"tray_ok":            a.trayOK,
		"cpu_sec":            math.Round(processCPUSeconds()*100) / 100,
		"data_dir":           a.cfg.DataDir,
	}
	for k, v := range memStats() {
		st[k] = v
	}
	for k, v := range resourceCounts() {
		st[k] = v
	}
	a.store.WriteStatus(st)
	a.lastStatusMono = time.Now()
}

// ---------------- 电源与会话 ----------------

func (a *App) onSuspend() {
	if a.suspended {
		return
	}
	a.suspended = true
	a.emitState("offline", "system_suspend")
	a.store.Log("system suspend")
	a.refreshTrayTip()
}

func (a *App) onResume() {
	if !a.suspended {
		return
	}
	a.suspended = false
	a.refreshForeground()
	a.refreshIdle()
	a.emitState("online", "system_resume")
	a.store.Log("system resume")
	a.refreshTrayTip()
}

// ---------------- 生命周期 ----------------

func (a *App) shutdown(reason string) {
	if a.quitFlag {
		return
	}
	a.quitFlag = true
	a.exitReason = reason
	if !a.paused {
		a.refreshForeground()
	}
	rec := a.stateRecord("stop", reason)
	rec.UptimeSec = floatPtr(math.Round(time.Since(a.startedMono).Seconds()))
	if a.paused {
		rec.Process = ""
		rec.Title = ""
		rec.WindowClass = ""
		rec.IdleSec = nil
		rec.Paused = true
	}
	a.emit(rec)
	a.writeStatus()
	a.store.Log("collector stop reason=" + reason + " records=" + itoa(a.store.Written()) +
		" dropped=" + itoa(a.store.Dropped()))
	a.removeTray()
	a.store.Close()
	procPostQuitMessage.Call(0)
}

func (a *App) openDataDir() {
	// explorer.exe 只认反斜杠；手改配置可能写成正斜杠，这里统一归一化并转绝对路径。
	dir, err := filepath.Abs(filepath.FromSlash(a.cfg.DataDir))
	if err != nil {
		a.store.Log("cannot resolve data dir: " + err.Error())
		return
	}
	if !fileExists(dir) {
		a.store.Log("data dir does not exist: " + dir)
		return
	}
	a.store.Log("opening data dir: " + dir)
	cmd := exec.Command("explorer.exe", dir)
	if err := cmd.Start(); err != nil {
		a.store.Log("cannot open data dir: " + err.Error())
		return
	}
	// explorer.exe 立刻交棒给已有实例后自己退出；不等它，但必须释放进程句柄，否则每次点击漏一个句柄
	if cmd.Process != nil {
		cmd.Process.Release()
	}
}

func (a *App) togglePause() {
	defer a.syncPause()
	if fileExists(a.pausePath) {
		if err := os.Remove(a.pausePath); err != nil {
			a.store.Log("cannot clear pause flag: " + err.Error())
			return
		}
	} else {
		content := time.Now().Format("2006-01-02T15:04:05-07:00") + newline
		if err := os.WriteFile(a.pausePath, []byte(content), 0o644); err != nil {
			a.store.Log("cannot set pause flag: " + err.Error())
		}
	}
}

func (a *App) stateText() string {
	switch {
	case a.paused:
		return "已暂停（不记录细节）"
	case a.suspended:
		return "系统休眠中"
	case a.locked:
		return "已锁屏"
	default:
		return "记录中"
	}
}

// lastRecordEpoch 读当天数据文件的最后一条记录时间，用来给「进程重启造成的空洞」打标。
// 只读文件尾部一小段，不整文件加载。
func lastRecordEpoch(dataDir string) float64 {
	// 跨日重启也要找到上一份记录。
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return 0
	}
	path := ""
	today := time.Now().Format("2006-01-02") + ".jsonl"
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && len(name) == len(today) && strings.HasSuffix(name, ".jsonl") && name <= today {
			path = filepath.Join(dataDir, name)
		}
	}
	if path == "" {
		return 0
	}
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.Size() == 0 {
		return 0
	}
	const tail = 8192
	start := int64(0)
	if fi.Size() > tail {
		start = fi.Size() - tail
	}
	buf := make([]byte, fi.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return 0
	}
	lines := strings.Split(string(buf), newline)
	for i := len(lines) - 1; i >= 0; i-- {
		s := strings.TrimSpace(lines[i])
		if s == "" {
			continue
		}
		var r Record
		if json.Unmarshal([]byte(s), &r) == nil && r.Epoch > 0 {
			return r.Epoch
		}
	}
	return 0
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// memStats 暴露 Go 运行时的真实内存数字，便于长期盯有无泄漏。
// resourceCounts 取本进程的内核句柄数与 GDI/USER 对象数，用于长期盯托盘相关泄漏。
func resourceCounts() map[string]any {
	out := map[string]any{}
	cur, _, _ := procGetCurrentProcess.Call()
	var handles uint32
	if ok, _, _ := procGetProcessHandleCount.Call(cur, uintptr(unsafe.Pointer(&handles))); ok != 0 {
		out["handles"] = handles
	}
	if v, _, _ := procGetGuiResources.Call(cur, grGdiObjects); v != 0 {
		out["gdi_objects"] = uint32(v)
	}
	if v, _, _ := procGetGuiResources.Call(cur, grUserObjects); v != 0 {
		out["user_objects"] = uint32(v)
	}
	return out
}

func memStats() map[string]any {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	const mb = 1048576.0
	return map[string]any{
		"go_heap_alloc_mb": round2(float64(m.HeapAlloc) / mb),
		"go_heap_sys_mb":   round2(float64(m.HeapSys) / mb),
		"go_stack_mb":      round2(float64(m.StackInuse) / mb),
		"go_total_sys_mb":  round2(float64(m.Sys) / mb),
		"go_num_gc":        m.NumGC,
		"go_heap_objects":  m.HeapObjects,
	}
}

func singleInstance() (uintptr, error) {
	name := u16Ptr(mutexName)
	h, _, err := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return 0, err
	}
	// ERROR_ALREADY_EXISTS 时 CreateMutex 仍返回句柄，需要读 last error
	if e, ok := err.(syscall.Errno); ok && uintptr(e) == errorAlreadyExists {
		return 0, err
	}
	return h, nil
}
