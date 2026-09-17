package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	os.Exit(realMain(os.Args[1:]))
}

func ftoa(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func realMain(args []string) int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot locate executable:", err)
		return 2
	}
	exeDir := filepath.Dir(exe)

	cmd := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
		args = args[1:]
	}

	fs := flag.NewFlagSet("collector", flag.ContinueOnError)
	configPath := fs.String("config", filepath.Join(exeDir, "config.json"), "配置文件路径")
	dataDir := fs.String("data-dir", "", "覆盖数据目录")
	logDir := fs.String("log-dir", "", "覆盖日志目录")
	idleThreshold := fs.Float64("idle-threshold", 0, "覆盖空闲判定阈值（秒）")
	maxSeconds := fs.Float64("max-seconds", 0, "跑满多少秒后正常退出（0 表示不限）")
	duration := fs.Float64("duration", 20, "bench 参数：采样多少秒")
	headless := fs.Bool("headless", false, "不建托盘窗口（排错用）")
	noTray := fs.Bool("no-tray", false, "不建托盘图标")
	verbose := fs.Bool("verbose", false, "把每条记录同时打到 stdout")
	diag := fs.Bool("diag", false, "诊断模式：跳过单实例保护（仅用于排查，绝不用于开机自启）")
	dateFlag := fs.String("date", "", "review：日期 YYYY-MM-DD，默认今天")
	jsonOut := fs.Bool("json", false, "review/status：输出 JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := loadConfig(exeDir, *configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "配置读取失败:", err)
		return 2
	}
	if *dataDir != "" {
		cfg.DataDir = *dataDir
	}
	if *logDir != "" {
		cfg.LogDir = *logDir
	}
	if *idleThreshold > 0 {
		cfg.IdleThresholdSec = *idleThreshold
	}

	switch cmd {
	case "run":
		return cmdRun(cfg, exeDir, *headless || *noTray, *verbose, *maxSeconds, *diag)
	case "once":
		return cmdOnce(cfg)
	case "bench":
		return cmdBench(cfg, *duration)
	case "review":
		return cmdReview(cfg, *dateFlag, *jsonOut)
	case "status":
		return cmdStatus(cfg, *jsonOut)
	case "pause":
		return cmdPause(exeDir, true)
	case "resume":
		return cmdPause(exeDir, false)
	case "version":
		fmt.Println("screentimeobserver collector 1.0 (go, stdlib only)")
		return 0
	default:
		fmt.Fprintln(os.Stderr, "未知子命令:", cmd)
		fmt.Fprintln(os.Stderr, "可用: run | once | bench | review | status | pause | resume | version")
		return 2
	}
}

func cmdRun(cfg Config, exeDir string, headless, verbose bool, maxSeconds float64, diag bool) int {
	if diag {
		fmt.Fprintln(os.Stderr, "诊断模式：跳过单实例保护；此实例不参与开机自启")
	} else {
		h, _ := singleInstance()
		if h == 0 {
			fmt.Fprintln(os.Stderr, "已有采集器实例在运行，本次退出。")
			return 3
		}
	}
	app := newApp(cfg, exeDir, headless, verbose)
	appInstance = app
	return app.run(maxSeconds)
}

func cmdOnce(cfg Config) int {
	s := sampleForeground()
	out := map[string]any{
		"ts":                 time.Now().Format("2006-01-02T15:04:05.000-07:00"),
		"process":            s.Process,
		"pid":                s.PID,
		"title":              s.Title,
		"window_class":       s.WindowClass,
		"idle_sec":           nanSeconds(idleSeconds()),
		"locked":             sessionLocked(),
		"idle_threshold_sec": cfg.IdleThresholdSec,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
	return 0
}

func cmdBench(cfg Config, seconds float64) int {
	if seconds <= 0 {
		seconds = 20
	}
	cpu0 := processCPUSeconds()
	start := time.Now()
	var n int
	var maxIdle, maxFg, sumIdle, sumFg float64
	for time.Since(start).Seconds() < seconds {
		t0 := time.Now()
		idleSeconds()
		d := float64(time.Since(t0).Microseconds())
		sumIdle += d
		if d > maxIdle {
			maxIdle = d
		}
		t0 = time.Now()
		sampleForeground()
		d = float64(time.Since(t0).Microseconds())
		sumFg += d
		if d > maxFg {
			maxFg = d
		}
		n++
		time.Sleep(time.Second)
	}
	wall := time.Since(start).Seconds()
	cpu := processCPUSeconds() - cpu0
	out := map[string]any{
		"samples":                      n,
		"wall_sec":                     int(wall),
		"idle_sample_us_avg":           sumIdle / float64(n),
		"idle_sample_us_max":           maxIdle,
		"foreground_sample_us_avg":     sumFg / float64(n),
		"foreground_sample_us_max":     maxFg,
		"note":                         "前台窗口采样只在事件/对账时发生；这里按每秒一次的最坏情况试算",
		"cpu_sec_used":                 cpu,
		"cpu_percent_of_wall":          100 * cpu / wall,
		"cpu_sec_per_day_at_this_rate": cpu / wall * 86400,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
	return 0
}

func cmdStatus(cfg Config, asJSON bool) int {
	p := filepath.Join(cfg.LogDir, "status.json")
	b, err := os.ReadFile(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读不到状态文件（采集器可能没在跑）:", err)
		return 1
	}
	if asJSON {
		fmt.Println(string(b))
		return 0
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		fmt.Println(string(b))
		return 0
	}
	keys := []string{"updated", "uptime_sec", "paused", "session", "idle", "idle_sec",
		"foreground_process", "foreground_title", "records_written", "records_dropped",
		"foreground_events", "hook_ok", "tray_ok", "cpu_sec", "screenshot_enabled",
		"screenshots_written", "screenshot_failures", "last_screenshot_ts", "screenshot_dir", "data_dir"}
	for _, k := range keys {
		fmt.Printf("%-20s %v\n", k, v[k])
	}

	// 开机自启检查：把「系统本次开机时间」与「采集器启动时间」摆在一起，一眼看出有没有自动起来
	sysUp := 0.0
	if ms, _, _ := procGetTickCount64.Call(); ms != 0 {
		sysUp = float64(ms) / 1000.0
	}
	bootAt := time.Now().Add(-time.Duration(sysUp) * time.Second)
	fmt.Println()
	fmt.Println("—— 开机自启检查 ——")
	fmt.Printf("系统本次开机       %s（已开机 %s）\n", bootAt.Format("2006-01-02 15:04:05"), hm(sysUp))
	if s, ok := v["started_at"].(string); ok {
		if t, err := time.Parse("2006-01-02T15:04:05-07:00", s); err == nil {
			if t.After(bootAt) {
				fmt.Printf("采集器本次启动     %s  → 本次开机后启动；仅凭此时间不能证明自启成功\n", t.Format("2006-01-02 15:04:05"))
			} else {
				fmt.Printf("采集器本次启动     %s  → 状态早于本次开机，需检查进程与状态更新时间\n", t.Format("2006-01-02 15:04:05"))
			}
		}
	}
	fmt.Println("自启配置需由 Windows 计划任务与启动项核验；此处不推断触发来源")
	fmt.Println("数据目录           " + cfg.DataDir)
	return 0
}

func cmdPause(exeDir string, pause bool) int {
	p := filepath.Join(exeDir, "paused.flag")
	if pause {
		content := time.Now().Format("2006-01-02T15:04:05-07:00") + newline
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "写入暂停标志失败:", err)
			return 1
		}
		fmt.Println("已暂停采集（采集器最迟 1 秒内生效；暂停期间不记录任何窗口细节）")
		return 0
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "清除暂停标志失败:", err)
		return 1
	}
	fmt.Println("已恢复采集")
	return 0
}
