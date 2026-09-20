package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"
)

func TestWLANStructLayout(t *testing.T) {
	if got := unsafe.Sizeof(wlanInterfaceInfo{}); got != 532 {
		t.Fatalf("WLAN_INTERFACE_INFO size=%d, want 532", got)
	}
	if got := unsafe.Sizeof(wlanBSSEntry{}); got != 360 {
		t.Fatalf("WLAN_BSS_ENTRY size=%d, want 360", got)
	}
	var e wlanBSSEntry
	if got := unsafe.Offsetof(e.Timestamp); got != 72 {
		t.Fatalf("timestamp offset=%d, want 72", got)
	}
	if got := unsafe.Offsetof(e.RateSet); got != 96 {
		t.Fatalf("rate set offset=%d, want 96", got)
	}
	if got := unsafe.Offsetof(e.IEOffset); got != 352 {
		t.Fatalf("IE offset=%d, want 352", got)
	}
}

func TestNetworkFromBSSPreservesSSIDBytes(t *testing.T) {
	e := wlanBSSEntry{RSSI: -52, LinkQuality: 81, CenterFrequency: 5180000, BSSID: [6]byte{0, 1, 2, 0xaa, 0xbb, 0xff}}
	raw := []byte{'A', 0xff, 'B'}
	e.SSID.Length = uint32(len(raw))
	copy(e.SSID.Value[:], raw)
	n := networkFromBSS(&e)
	if n.SSIDHex != "41ff42" {
		t.Fatalf("ssid_hex=%q", n.SSIDHex)
	}
	if n.BSSID != "00:01:02:aa:bb:ff" {
		t.Fatalf("bssid=%q", n.BSSID)
	}
	if n.Channel != 36 {
		t.Fatalf("channel=%d", n.Channel)
	}
}

func TestWiFiChannel(t *testing.T) {
	cases := map[uint32]int{2412000: 1, 2484000: 14, 5180000: 36, 5955000: 1, 123: 0}
	for khz, want := range cases {
		if got := wifiChannel(khz); got != want {
			t.Errorf("wifiChannel(%d)=%d want %d", khz, got, want)
		}
	}
}

func TestCleanupWiFiFilesKeepsRollingWindowAndUnmanagedFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.Local)
	for _, name := range []string{"2026-09-01.jsonl", "2026-09-02.jsonl", "2026-09-03.jsonl", "enabled.flag", "notes.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := cleanupWiFiFiles(dir, now, 15)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed=%d want 1", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-09-01.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("old file not removed: %v", err)
	}
	for _, name := range []string{"2026-09-02.jsonl", "2026-09-03.jsonl", "enabled.flag", "notes.jsonl"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s should remain: %v", name, err)
		}
	}
}

func TestAppendWiFiRecordJSONL(t *testing.T) {
	dir := t.TempDir()
	rec := WiFiRecord{TS: "2026-09-17T12:34:56.000+08:00", Epoch: 1, Event: "periodic", Source: "cache", Status: "ok", Interfaces: []WiFiInterface{}}
	if err := appendWiFiRecord(dir, rec); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "2026-09-17.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 || b[len(b)-1] != '\n' {
		t.Fatalf("not newline-delimited: %q", b)
	}
}

// TestWiFiManagerOpen 只验证 wlanapi 句柄和回调注册，不读取 BSSID，也不会触发位置授权。
func TestWiFiManagerOpen(t *testing.T) {
	if os.Getenv("SCT_WIFI_OPEN_TEST") != "1" {
		t.Skip("设置 SCT_WIFI_OPEN_TEST=1 才访问本机 WLAN 服务")
	}
	m, err := openWiFiManager()
	if err != nil {
		t.Fatal(err)
	}
	m.close()
}

// TestWiFiSnapshot 会读取受 Windows 精确位置权限保护的 BSSID，仅在人工排错时显式运行。
func TestWiFiSnapshot(t *testing.T) {
	if os.Getenv("SCT_WIFI_SNAPSHOT_TEST") != "1" {
		t.Skip("设置 SCT_WIFI_SNAPSHOT_TEST=1 才读取本机 Wi-Fi")
	}
	m, err := openWiFiManager()
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	ifs, err := m.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("interfaces=%d", len(ifs))
}
