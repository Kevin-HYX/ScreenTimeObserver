package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type segRow struct {
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Dur     float64 `json:"dur_sec"`
	Kind    string  `json:"kind"`
	Process string  `json:"process"`
	Title   string  `json:"title"`
	Reason  string  `json:"reason"`
}

type nameSec struct {
	Name string  `json:"name"`
	Sec  float64 `json:"sec"`
}

func hm(sec float64) string {
	s := int(sec)
	return fmt.Sprintf("%dh%02dm%02ds", s/3600, (s%3600)/60, s%60)
}

// cmdReview 复看某一天的数据：连续性、空洞、重复，以及时间到底花在哪。
func cmdReview(cfg Config, date string, asJSON bool) int {
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	path := filepath.Join(cfg.DataDir, date+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "打不开数据文件:", err)
		return 1
	}
	defer f.Close()

	var recs []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	badLines := 0
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(line, &r); err != nil {
			badLines++
			continue
		}
		recs = append(recs, r)
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "读取数据失败:", err)
		return 1
	}
	if len(recs) == 0 {
		fmt.Println("该日无记录:", path)
		return 1
	}

	kindOf := func(r Record) string {
		switch {
		case r.Paused:
			return "paused"
		case r.Session == "suspended":
			return "suspended"
		case r.Locked:
			return "locked"
		case r.Idle:
			return "idle"
		default:
			return "active"
		}
	}

	var segs []segRow
	byKind := map[string]float64{}
	byProc := map[string]float64{}
	for i := 0; i+1 < len(recs); i++ {
		d := recs[i+1].Epoch - recs[i].Epoch
		if d < 0 {
			d = 0
		}
		k := kindOf(recs[i])
		if recs[i].Event == "stop" || (d > cfg.GapReportSec && k != "suspended") {
			k = "unknown"
		}
		segs = append(segs, segRow{
			Start: recs[i].Epoch, End: recs[i+1].Epoch, Dur: d, Kind: k,
			Process: recs[i].Process, Title: recs[i].Title, Reason: recs[i].Reason,
		})
		byKind[k] += d
		if k == "active" && recs[i].Process != "" {
			byProc[recs[i].Process] += d
		}
	}

	last := recs[len(recs)-1]
	openDur := 0.0
	// 未观测的尾部不推算为使用时长；历史日期尤其不能延长至今天。
	_ = last

	gaps := []segRow{}
	for _, s := range segs {
		if s.Dur > cfg.GapReportSec || s.Kind == "unknown" {
			gaps = append(gaps, s)
		}
	}
	holeSec := 0.0
	for _, g := range gaps {
		if g.Kind != "suspended" {
			holeSec += g.Dur
		}
	}

	dups := 0
	for i := 1; i < len(recs); i++ {
		p, c := recs[i-1], recs[i]
		if c.Reason == "heartbeat" || c.Event == "carry_over" || (c.Event == "paused" && p.Paused) {
			continue
		}
		if p.Process == c.Process && p.Title == c.Title && p.Idle == c.Idle &&
			p.Locked == c.Locked && p.Session == c.Session && p.Paused == c.Paused &&
			c.Event != "start" && c.Event != "online" && c.Event != "stop" {
			dups++
		}
	}

	var top []nameSec
	for k, v := range byProc {
		top = append(top, nameSec{k, v})
	}
	sort.Slice(top, func(i, j int) bool { return top[i].Sec > top[j].Sec })
	if len(top) > 12 {
		top = top[:12]
	}
	var kindList []nameSec
	for k, v := range byKind {
		kindList = append(kindList, nameSec{k, v})
	}
	sort.Slice(kindList, func(i, j int) bool { return kindList[i].Sec > kindList[j].Sec })

	span := recs[len(recs)-1].Epoch - recs[0].Epoch

	if asJSON {
		out := map[string]any{
			"date":              date,
			"file":              path,
			"records":           len(recs),
			"bad_lines":         badLines,
			"first_ts":          recs[0].TS,
			"last_ts":           recs[len(recs)-1].TS,
			"span_sec":          span,
			"coverage_sec":      span - holeSec,
			"gap_count":         len(gaps),
			"gap_sec_total":     holeSec,
			"gaps":              gaps,
			"duplicate_records": dups,
			"by_kind":           kindList,
			"top_processes":     top,
			"open_tail_sec":     openDur,
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return 0
	}

	fmt.Println("日期:", date)
	fmt.Println("文件:", path)
	fmt.Printf("记录条数: %d（坏行 %d）\n", len(recs), badLines)
	fmt.Printf("时间跨度: %s → %s（%s）\n", recs[0].TS, recs[len(recs)-1].TS, hm(span))
	fmt.Println()
	fmt.Println("—— 连续性 ——")
	fmt.Printf("空洞数（相邻记录间隔 > %.0f 秒）: %d，其中真空洞合计 %s\n",
		cfg.GapReportSec, len(gaps), hm(holeSec))
	lim := len(gaps)
	if lim > 10 {
		lim = 10
	}
	for _, g := range gaps[:lim] {
		fmt.Printf("  %s 起 %s —— 状态=%s%s（reason=%s）\n",
			time.Unix(int64(g.Start), 0).Format("15:04:05"), hm(g.Dur),
			g.Kind, procSuffix(g.Process), g.Reason)
	}
	if len(gaps) > lim {
		fmt.Printf("  ...另有 %d 段\n", len(gaps)-lim)
	}
	fmt.Printf("疑似重复记录: %d\n", dups)
	fmt.Println()
	fmt.Println("—— 状态分布 ——")
	for _, e := range kindList {
		fmt.Printf("  %-10s %s\n", e.Name, hm(e.Sec))
	}
	fmt.Println()
	fmt.Println("—— 前台应用占用（仅 active 部分）——")
	for _, e := range top {
		fmt.Printf("  %-28s %s\n", e.Name, hm(e.Sec))
	}
	if openDur > 0 {
		fmt.Printf("\n末条记录仍在延续中：%.0f 秒（采集器仍在运行）\n", openDur)
	}
	return 0
}

func procSuffix(p string) string {
	if p == "" {
		return ""
	}
	return " " + p
}
