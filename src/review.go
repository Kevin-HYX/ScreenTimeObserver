package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

func hm(sec float64) string {
	s := int(sec)
	return fmt.Sprintf("%dh%02dm%02ds", s/3600, (s%3600)/60, s%60)
}

// pln 打印一行。格式串里不写换行转义，避免转义在不同工具链里被改写。
func pln(format string, args ...any) {
	fmt.Printf(format, args...)
	fmt.Println()
}

// cmdReview 复看某一天的数据：连续性、空洞、重复，以及时间到底花在哪。
func cmdReview(cfg Config, date string, asJSON bool) int {
	if date == "" {
		date = today()
	}
	if !validDate(date) {
		fmt.Fprintln(os.Stderr, "日期格式必须为 YYYY-MM-DD:", date)
		return 2
	}
	recs, badLines, path, err := loadDayRecords(cfg.DataDir, date)
	if err != nil {
		fmt.Fprintln(os.Stderr, "打不开数据文件:", err)
		return 1
	}
	if len(recs) == 0 {
		fmt.Println("该日无记录:", path)
		return 1
	}
	a := analyzeDay(date, recs, badLines, path, cfg.GapReportSec)

	if asJSON {
		b, _ := json.MarshalIndent(a.summaryJSON(12, 0), "", "  ")
		fmt.Println(string(b))
		return 0
	}

	fmt.Println("日期:", date)
	fmt.Println("文件:", a.Path)
	pln("记录条数: %d（坏行 %d）", a.Records, a.BadLines)
	pln("时间跨度: %s → %s（%s）", a.FirstTS, a.LastTS, hm(a.SpanSec))
	fmt.Println()
	fmt.Println("—— 连续性 ——")
	pln("已观测 %s，空洞 %s，休眠未观测 %s", hm(a.CoveredSec), hm(a.HoleSec), hm(a.SleepSec))
	pln("空洞数（相邻记录间隔 > %.0f 秒）: %d", cfg.GapReportSec, len(a.Gaps))
	lim := len(a.Gaps)
	if lim > 10 {
		lim = 10
	}
	for _, g := range a.Gaps[:lim] {
		pln("  %s 起 %s —— 状态=%s%s（reason=%s）",
			time.Unix(int64(g.Start), 0).Format("15:04:05"), hm(g.Dur),
			g.Kind, procSuffix(g.Process), g.Reason)
	}
	if len(a.Gaps) > lim {
		pln("  ...另有 %d 段", len(a.Gaps)-lim)
	}
	pln("疑似重复记录: %d", a.Dups)
	fmt.Println()
	fmt.Println("—— 状态分布（仅已观测部分）——")
	for _, e := range a.ByKind {
		pln("  %-10s %s", e.Name, hm(e.Sec))
	}
	fmt.Println()
	fmt.Println("—— 前台应用占用（操作 + 观看）——")
	for _, e := range topN(a.ByProcess, 12) {
		pln("  %-28s %s", e.Name, hm(e.Sec))
	}
	return 0
}

func procSuffix(p string) string {
	if p == "" {
		return ""
	}
	return " " + p
}
