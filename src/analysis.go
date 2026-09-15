package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// segRow 是一段连续状态：从 Start 到 End 之间状态不变。
type segRow struct {
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Dur     float64 `json:"dur_sec"`
	Kind    string  `json:"kind"`
	Process string  `json:"process"`
	Title   string  `json:"title"`
	Reason  string  `json:"reason"`
	Event   string  `json:"event"`
}

type nameSec struct {
	Name string  `json:"name"`
	Sec  float64 `json:"sec"`
}

// DayAnalysis 是某一天数据的汇总。CLI 复看与 MCP 工具共用这一套判定口径，
// 避免两处各写一份「什么算活跃、什么算空洞」的规则。
type DayAnalysis struct {
	Date         string
	Path         string
	Records      int
	BadLines     int
	FirstTS      string
	LastTS       string
	SpanSec      float64
	CoveredSec   float64
	HoleSec      float64
	SleepSec     float64
	Segments     []segRow
	Gaps         []segRow
	ByKind       []nameSec
	ByProcess    []nameSec
	Dups         int
	kinds        map[string]float64
	procs        map[string]float64
	CoveredTotal float64
}

func validDate(s string) bool {
	if len(s) != 10 {
		return false
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

func today() string {
	return time.Now().Format("2006-01-02")
}

// dayStartEpoch 返回某天 00:00:00 的本机时间戳，用于把 HH:MM 换算成绝对时间。
func dayStartEpoch(date string) (float64, bool) {
	t, err := time.ParseInLocation("2006-01-02", date, time.Local)
	if err != nil {
		return 0, false
	}
	return float64(t.UnixNano()) / 1e9, true
}

// loadDayRecords 读取一天的数据文件。坏行单独计数，不静默吞掉。
func loadDayRecords(dataDir, date string) ([]Record, int, string, error) {
	path := filepath.Join(dataDir, date+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, path, err
	}
	defer f.Close()
	var recs []Record
	bad := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(line, &r); err != nil {
			bad++
			continue
		}
		if r.Epoch <= 0 {
			bad++
			continue
		}
		recs = append(recs, r)
	}
	if err := sc.Err(); err != nil {
		return recs, bad, path, err
	}
	return recs, bad, path, nil
}

// dayFiles 列出数据目录里的日期文件（YYYY-MM-DD.jsonl），按日期升序。
func dayFiles(dataDir string) []string {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) != len("2006-01-02.jsonl") || filepath.Ext(name) != ".jsonl" {
			continue
		}
		d := name[:10]
		if !validDate(d) {
			continue
		}
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

func kindOf(r Record) string {
	switch {
	case r.Paused:
		return "paused"
	case r.Session == "suspended":
		return "suspended"
	case r.Locked:
		return "locked"
	case r.Media:
		return "media"
	case r.Idle:
		return "idle"
	default:
		return "active"
	}
}

// segmentKind 给一段区间定性质。停止记录之后、或间隔超过空洞阈值，都算没观测到。
func segmentKind(r Record, dur, gapSec float64) string {
	if r.Event == "stop" {
		return "unknown"
	}
	k := kindOf(r)
	if dur > gapSec && k != "suspended" {
		return "unknown"
	}
	return k
}

func sortedSec(m map[string]float64) []nameSec {
	out := make([]nameSec, 0, len(m))
	for k, v := range m {
		out = append(out, nameSec{Name: k, Sec: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sec == out[j].Sec {
			return out[i].Name < out[j].Name
		}
		return out[i].Sec > out[j].Sec
	})
	return out
}

// countDuplicates 统计「状态没变却写了一条」的可疑记录；心跳与预期事件不计入。
func countDuplicates(recs []Record) int {
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
	return dups
}

func analyzeDay(date string, recs []Record, badLines int, path string, gapReportSec float64) DayAnalysis {
	a := DayAnalysis{
		Date: date, Path: path, Records: len(recs), BadLines: badLines,
		kinds: map[string]float64{}, procs: map[string]float64{},
	}
	if len(recs) == 0 {
		return a
	}
	a.FirstTS = recs[0].TS
	a.LastTS = recs[len(recs)-1].TS
	a.SpanSec = recs[len(recs)-1].Epoch - recs[0].Epoch

	hole, sleep := 0.0, 0.0
	for i := 0; i+1 < len(recs); i++ {
		d := recs[i+1].Epoch - recs[i].Epoch
		if d < 0 {
			d = 0
		}
		k := segmentKind(recs[i], d, gapReportSec)
		seg := segRow{
			Start: recs[i].Epoch, End: recs[i+1].Epoch, Dur: d, Kind: k,
			Process: recs[i].Process, Title: recs[i].Title,
			Reason: recs[i].Reason, Event: recs[i].Event,
		}
		a.Segments = append(a.Segments, seg)

		if d > gapReportSec || k == "unknown" {
			a.Gaps = append(a.Gaps, seg)
			if k == "suspended" {
				sleep += d
			} else {
				hole += d
			}
			continue
		}
		// 只有真正观测到的区间才计入统计口径
		a.CoveredSec += d
		a.kinds[k] += d
		// 观看（media）同样是「人在场」，一起计入应用占用；纯操作与观看在 by_kind 里分开看。
		if (k == "active" || k == "media") && recs[i].Process != "" {
			a.procs[recs[i].Process] += d
		}
	}
	a.HoleSec = hole
	a.SleepSec = sleep
	if unknown := hole + sleep; unknown > 0 {
		a.kinds["unknown"] += unknown
	}
	a.Dups = countDuplicates(recs)
	a.ByKind = sortedSec(a.kinds)
	a.ByProcess = sortedSec(a.procs)
	return a
}

func topN(list []nameSec, n int) []nameSec {
	if n <= 0 || n > len(list) {
		n = len(list)
	}
	return list[:n]
}

// summaryJSON 是复看与 MCP 共用的汇总结构。
func (a DayAnalysis) summaryJSON(topProcesses, maxGaps int) map[string]any {
	out := map[string]any{
		"date":              a.Date,
		"file":              a.Path,
		"records":           a.Records,
		"bad_lines":         a.BadLines,
		"first_ts":          a.FirstTS,
		"last_ts":           a.LastTS,
		"span_sec":          a.SpanSec,
		"coverage_sec":      a.CoveredSec,
		"gap_count":         len(a.Gaps),
		"gap_sec_total":     a.HoleSec,
		"uncovered_sec":     a.HoleSec + a.SleepSec,
		"duplicate_records": a.Dups,
		"by_kind":           a.ByKind,
		"top_processes":     topN(a.ByProcess, topProcesses),
	}
	if maxGaps > 0 {
		out["gaps"] = topN2(a.Gaps, maxGaps)
	}
	return out
}

func topN2(list []segRow, n int) []segRow {
	if n <= 0 || n > len(list) {
		n = len(list)
	}
	return list[:n]
}
