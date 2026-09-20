package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"
)

const (
	wlanClientVersionLonghorn          = 2
	wlanNotificationSourceNone         = 0
	wlanNotificationSourceACM          = 0x00000008
	wlanNotificationScanComplete       = 7
	wlanNotificationScanFail           = 8
	wlanNotificationConnectionComplete = 10
	wlanNotificationInterfaceArrival   = 13
	wlanNotificationInterfaceRemoval   = 14
	wlanNotificationDisconnected       = 21
	wlanInterfaceStateConnected        = 1
	dot11BSSTypeAny                    = 3
	errorAccessDenied                  = 5
	errorServiceNotActive              = 1062
	errorNDISDot11PowerStateInvalid    = 0x80342002
)

var (
	wlanapi                      = syscall.NewLazyDLL("wlanapi.dll")
	procWlanOpenHandle           = wlanapi.NewProc("WlanOpenHandle")
	procWlanCloseHandle          = wlanapi.NewProc("WlanCloseHandle")
	procWlanEnumInterfaces       = wlanapi.NewProc("WlanEnumInterfaces")
	procWlanGetNetworkBssList    = wlanapi.NewProc("WlanGetNetworkBssList")
	procWlanScan                 = wlanapi.NewProc("WlanScan")
	procWlanFreeMemory           = wlanapi.NewProc("WlanFreeMemory")
	procWlanRegisterNotification = wlanapi.NewProc("WlanRegisterNotification")
)

type wlanGUID struct {
	Data1        uint32
	Data2, Data3 uint16
	Data4        [8]byte
}
type wlanInterfaceInfo struct {
	GUID        wlanGUID
	Description [256]uint16
	State       uint32
}
type dot11SSID struct {
	Length uint32
	Value  [32]byte
}
type wlanRateSet struct {
	Length uint32
	Rate   [126]uint16
}

// wlanBSSEntry 使用显式填充固定 Windows SDK 的字段偏移；测试会锁定大小和关键偏移。
type wlanBSSEntry struct {
	SSID            dot11SSID
	PhyID           uint32
	BSSID           [6]byte
	_pad0           [2]byte
	BSSType         uint32
	PhyType         uint32
	RSSI            int32
	LinkQuality     uint32
	InRegDomain     byte
	_pad1           byte
	BeaconPeriod    uint16
	_pad2           [4]byte
	Timestamp       uint64
	HostTimestamp   uint64
	Capability      uint16
	_pad3           [2]byte
	CenterFrequency uint32
	RateSet         wlanRateSet
	IEOffset        uint32
	IESize          uint32
}

type wlanNotificationData struct {
	Source   uint32
	Code     uint32
	GUID     wlanGUID
	DataSize uint32
	_pad     [4]byte
	Data     uintptr
}

type WiFiNetwork struct {
	SSID         string `json:"ssid"`
	SSIDHex      string `json:"ssid_hex"`
	BSSID        string `json:"bssid"`
	RSSI         int32  `json:"rssi_dbm"`
	Quality      uint32 `json:"quality"`
	FrequencyKHz uint32 `json:"frequency_khz"`
	Channel      int    `json:"channel,omitempty"`
}

type WiFiInterface struct {
	GUID        string        `json:"guid"`
	Description string        `json:"description"`
	State       string        `json:"state"`
	Networks    []WiFiNetwork `json:"networks"`
}

type WiFiRecord struct {
	TS         string          `json:"ts"`
	Epoch      float64         `json:"epoch"`
	Event      string          `json:"event"`
	Source     string          `json:"source"`
	Status     string          `json:"status"`
	Error      string          `json:"error,omitempty"`
	Interfaces []WiFiInterface `json:"interfaces"`
}

type wifiManager struct {
	handle   uintptr
	callback uintptr
	mu       sync.Mutex
}

func openWiFiManager() (*wifiManager, error) {
	var negotiated uint32
	var handle uintptr
	code, _, _ := procWlanOpenHandle.Call(wlanClientVersionLonghorn, 0,
		uintptr(unsafe.Pointer(&negotiated)), uintptr(unsafe.Pointer(&handle)))
	if code != 0 {
		return nil, wlanCallError("WlanOpenHandle", code)
	}
	m := &wifiManager{handle: handle}
	m.callback = syscall.NewCallback(wifiNotificationCallback)
	var previous uint32
	code, _, _ = procWlanRegisterNotification.Call(handle, wlanNotificationSourceACM, 1,
		m.callback, 0, 0, uintptr(unsafe.Pointer(&previous)))
	if code != 0 {
		procWlanCloseHandle.Call(handle, 0)
		return nil, wlanCallError("WlanRegisterNotification", code)
	}
	return m, nil
}

func (m *wifiManager) close() {
	if m == nil || m.handle == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var previous uint32
	procWlanRegisterNotification.Call(m.handle, wlanNotificationSourceNone, 1, 0, 0, 0,
		uintptr(unsafe.Pointer(&previous)))
	procWlanCloseHandle.Call(m.handle, 0)
	m.handle = 0
}

func wifiNotificationCallback(data unsafe.Pointer, context uintptr) uintptr {
	defer func() { _ = recover() }()
	if data == nil || appInstance == nil || appInstance.hwnd == 0 {
		return 0
	}
	n := (*wlanNotificationData)(data)
	if n.Source&wlanNotificationSourceACM != 0 {
		procPostMessageW.Call(appInstance.hwnd, wmWiFiEvent, uintptr(n.Code), 0)
	}
	return 0
}

func (m *wifiManager) interfaces() ([]wlanInterfaceInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.handle == 0 {
		return nil, errors.New("WLAN handle 已关闭")
	}
	var p unsafe.Pointer
	code, _, _ := procWlanEnumInterfaces.Call(m.handle, 0, uintptr(unsafe.Pointer(&p)))
	if code != 0 {
		return nil, wlanCallError("WlanEnumInterfaces", code)
	}
	if p == nil {
		return nil, errors.New("WlanEnumInterfaces 返回空指针")
	}
	defer procWlanFreeMemory.Call(uintptr(p))
	count := *(*uint32)(p)
	if count == 0 {
		return nil, errNoWiFiInterface
	}
	base := unsafe.Add(p, 8)
	result := make([]wlanInterfaceInfo, 0, count)
	step := unsafe.Sizeof(wlanInterfaceInfo{})
	for i := uint32(0); i < count; i++ {
		result = append(result, *(*wlanInterfaceInfo)(unsafe.Add(base, uintptr(i)*step)))
	}
	return result, nil
}

func (m *wifiManager) requestScan() error {
	ifs, err := m.interfaces()
	if err != nil {
		return err
	}
	var firstErr error
	success := false
	for i := range ifs {
		m.mu.Lock()
		code, _, _ := procWlanScan.Call(m.handle, uintptr(unsafe.Pointer(&ifs[i].GUID)), 0, 0, 0)
		m.mu.Unlock()
		if code == 0 {
			success = true
		} else if firstErr == nil {
			firstErr = wlanCallError("WlanScan", code)
		}
	}
	if success {
		return nil
	}
	if firstErr != nil {
		return firstErr
	}
	return errNoWiFiInterface
}

func (m *wifiManager) snapshot() ([]WiFiInterface, error) {
	ifs, err := m.interfaces()
	if err != nil {
		return nil, err
	}
	result := make([]WiFiInterface, 0, len(ifs))
	var firstErr error
	for i := range ifs {
		out := WiFiInterface{GUID: formatGUID(ifs[i].GUID), Description: syscall.UTF16ToString(ifs[i].Description[:]),
			State: wlanStateText(ifs[i].State), Networks: []WiFiNetwork{}}
		m.mu.Lock()
		var list unsafe.Pointer
		code, _, _ := procWlanGetNetworkBssList.Call(m.handle, uintptr(unsafe.Pointer(&ifs[i].GUID)),
			0, dot11BSSTypeAny, 0, 0, uintptr(unsafe.Pointer(&list)))
		if code == 0 && list != nil {
			count := *(*uint32)(unsafe.Add(list, 4))
			base := unsafe.Add(list, 8)
			step := unsafe.Sizeof(wlanBSSEntry{})
			for j := uint32(0); j < count; j++ {
				entry := (*wlanBSSEntry)(unsafe.Add(base, uintptr(j)*step))
				out.Networks = append(out.Networks, networkFromBSS(entry))
			}
			procWlanFreeMemory.Call(uintptr(list))
		}
		m.mu.Unlock()
		if code != 0 && firstErr == nil {
			firstErr = wlanCallError("WlanGetNetworkBssList", code)
		}
		sort.Slice(out.Networks, func(i, j int) bool { return out.Networks[i].BSSID < out.Networks[j].BSSID })
		result = append(result, out)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].GUID < result[j].GUID })
	if firstErr != nil {
		return nil, firstErr
	}
	return result, nil
}

var errNoWiFiInterface = errors.New("未发现 Wi-Fi 适配器")

func wlanCallError(action string, code uintptr) error {
	switch code {
	case errorAccessDenied:
		return fmt.Errorf("%s: 位置权限被拒绝 (ERROR_ACCESS_DENIED)", action)
	case errorServiceNotActive:
		return fmt.Errorf("%s: WLAN AutoConfig 服务未运行", action)
	case errorNDISDot11PowerStateInvalid:
		return fmt.Errorf("%s: Wi-Fi 无线电已关闭", action)
	default:
		return fmt.Errorf("%s: Windows 错误 0x%X", action, code)
	}
}

func networkFromBSS(e *wlanBSSEntry) WiFiNetwork {
	n := int(e.SSID.Length)
	if n > len(e.SSID.Value) {
		n = len(e.SSID.Value)
	}
	raw := append([]byte(nil), e.SSID.Value[:n]...)
	name := string(raw)
	if !utf8.Valid(raw) {
		name = strings.ToValidUTF8(name, "�")
	}
	return WiFiNetwork{SSID: name, SSIDHex: hex.EncodeToString(raw),
		BSSID: fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", e.BSSID[0], e.BSSID[1], e.BSSID[2], e.BSSID[3], e.BSSID[4], e.BSSID[5]),
		RSSI:  e.RSSI, Quality: e.LinkQuality, FrequencyKHz: e.CenterFrequency, Channel: wifiChannel(e.CenterFrequency)}
}

func wifiChannel(khz uint32) int {
	mhz := int(khz / 1000)
	switch {
	case mhz == 2484:
		return 14
	case mhz >= 2412 && mhz <= 2472:
		return (mhz - 2407) / 5
	case mhz >= 5000 && mhz <= 5895:
		return (mhz - 5000) / 5
	case mhz >= 5955 && mhz <= 7115:
		return (mhz - 5950) / 5
	default:
		return 0
	}
}

func formatGUID(g wlanGUID) string {
	return fmt.Sprintf("{%08x-%04x-%04x-%02x%02x-%02x%02x%02x%02x%02x%02x}", g.Data1, g.Data2, g.Data3,
		g.Data4[0], g.Data4[1], g.Data4[2], g.Data4[3], g.Data4[4], g.Data4[5], g.Data4[6], g.Data4[7])
}

func wlanStateText(s uint32) string {
	if s == wlanInterfaceStateConnected {
		return "connected"
	}
	if name, ok := map[uint32]string{0: "not_ready", 2: "ad_hoc", 3: "disconnecting", 4: "disconnected", 5: "associating", 6: "discovering", 7: "authenticating"}[s]; ok {
		return name
	}
	return fmt.Sprintf("unknown_%d", s)
}

func classifyWiFiError(err error) string {
	if err == nil {
		return "ok"
	}
	s := err.Error()
	switch {
	case errors.Is(err, errNoWiFiInterface):
		return "no_interface"
	case strings.Contains(s, "ERROR_ACCESS_DENIED"):
		return "access_denied"
	case strings.Contains(s, "无线电已关闭"):
		return "radio_off"
	case strings.Contains(s, "服务未运行"):
		return "service_inactive"
	default:
		return "error"
	}
}

func appendWiFiRecord(dir string, rec WiFiRecord) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, rec.TS[:10]+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Write(append(b, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func cleanupWiFiFiles(dir string, now time.Time, retentionDays int) (int, error) {
	if retentionDays <= 0 {
		retentionDays = 15
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	cutoff := now.AddDate(0, 0, -retentionDays)
	removed := 0
	var firstErr error
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		day, err := time.ParseInLocation("2006-01-02", strings.TrimSuffix(e.Name(), ".jsonl"), now.Location())
		if err != nil || !day.Before(time.Date(cutoff.Year(), cutoff.Month(), cutoff.Day(), 0, 0, 0, 0, cutoff.Location())) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		} else {
			removed++
		}
	}
	return removed, firstErr
}

func newWiFiRecord(event, source, status string, ifs []WiFiInterface, err error) WiFiRecord {
	now := time.Now()
	rec := WiFiRecord{TS: now.Format("2006-01-02T15:04:05.000-07:00"), Epoch: float64(now.UnixMilli()) / 1000,
		Event: event, Source: source, Status: status, Interfaces: ifs}
	if rec.Interfaces == nil {
		rec.Interfaces = []WiFiInterface{}
	}
	if err != nil {
		rec.Error = err.Error()
	}
	return rec
}

func init() { runtime.KeepAlive(wlanapi) }
