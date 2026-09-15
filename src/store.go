package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

const newline = "\n"

// Record 是落盘的一行。字段全部显式写出，保证 schema 稳定、可直接被读取解析。
type Record struct {
	TS          string   `json:"ts"`
	Epoch       float64  `json:"epoch"`
	Event       string   `json:"event"`
	Reason      string   `json:"reason"`
	Session     string   `json:"session"`
	Process     string   `json:"process"`
	PID         uint32   `json:"pid"`
	Title       string   `json:"title"`
	WindowClass string   `json:"window_class"`
	IdleSec     *float64 `json:"idle_sec"`
	Idle        bool     `json:"idle"`
	Locked      bool     `json:"locked"`
	Paused      bool     `json:"paused"`
	SincePrev   *float64 `json:"since_prev_sec"`
	Gap         bool     `json:"gap"`
	UptimeSec   *float64 `json:"uptime_sec,omitempty"`
}

type Store struct {
	dataDir string
	logDir  string
	ch      chan Record
	done    chan struct{}

	written   atomic.Int64
	dropped   atomic.Int64
	lastWrite atomic.Value
	logMu     sync.Mutex
}

func newStore(dataDir, logDir string) (*Store, error) {
	for _, d := range []string{dataDir, logDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	s := &Store{
		dataDir: dataDir,
		logDir:  logDir,
		ch:      make(chan Record, 1024),
		done:    make(chan struct{}),
	}
	go s.loop()
	return s, nil
}

func (s *Store) Enqueue(r Record) {
	select {
	case s.ch <- r:
	default:
		// 队列满意味着磁盘严重阻塞：明确记录丢弃，绝不静默
		n := s.dropped.Add(1)
		s.Log("QUEUE FULL: dropped record #" + itoa(n) + " event=" + r.Event)
	}
}

func (s *Store) loop() {
	defer close(s.done)
	var fh *os.File
	day := ""
	for r := range s.ch {
		d := r.TS[:10]
		if fh == nil || d != day {
			if fh != nil {
				fh.Close()
				fh = nil
			}
			path := filepath.Join(s.dataDir, d+".jsonl")
			fresh := !fileExists(path)
			f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				s.dropped.Add(1)
				s.Log("OPEN FAILED " + path + ": " + err.Error())
				continue
			}
			fh, day = f, d
			if fresh {
				s.Log("data file created: " + path)
			}
		}
		b, err := json.Marshal(r)
		if err != nil {
			s.dropped.Add(1)
			s.Log("MARSHAL FAILED: " + err.Error())
			continue
		}
		if _, err := fh.Write(append(b, newline...)); err != nil {
			s.dropped.Add(1)
			s.Log("WRITE FAILED: " + err.Error())
			continue
		}
		if err := fh.Sync(); err != nil {
			s.dropped.Add(1)
			s.Log("SYNC FAILED: " + err.Error())
			continue
		}
		s.written.Add(1)
		s.lastWrite.Store(r.TS)
	}
	if fh != nil {
		fh.Close()
	}
}

func (s *Store) Close() {
	close(s.ch)
	<-s.done
}

func (s *Store) Written() int64 { return s.written.Load() }
func (s *Store) Dropped() int64 { return s.dropped.Load() }

func (s *Store) WriteStatus(v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return
	}
	tmp := filepath.Join(s.logDir, "status.json.tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return
	}
	os.Rename(tmp, filepath.Join(s.logDir, "status.json"))
}

func (s *Store) Log(msg string) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	path := filepath.Join(s.logDir, "collector.log")
	if fi, err := os.Stat(path); err == nil && fi.Size() > 2_000_000 {
		os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(time.Now().Format("2006-01-02T15:04:05-07:00") + " " + msg + newline)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [24]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
