package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

// 峰值超过这个值就认为设备正在出声。留一点余量，避免把底噪当成播放。
const audioPeakThreshold = 0.0002

// ifacePointer 把接口地址还原成指针。
// COM 接口地址是运行时给的整数，这里通过取地址再解引用的写法表达
// 「把这个整数当地址用」，直接写 unsafe.Pointer(p) 会被 go vet 判为可疑转换。
func ifacePointer(p uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&p))
}

// comCall 调用 COM 接口的方法：接口指针指向 vtable，方法按索引取，
// 第一个参数固定是接口指针本身。
func comCall(p uintptr, index int, args ...uintptr) uintptr {
	vtbl := *(*uintptr)(ifacePointer(p))
	fn := *(*uintptr)(ifacePointer(vtbl + uintptr(index)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{p}, args...)...)
	return r
}

func comRelease(p uintptr) {
	if p != 0 {
		comCall(p, 2) // IUnknown::Release
	}
}

func hresultError(what string, hr uintptr) error {
	return fmt.Errorf("%s 失败，HRESULT=0x%08X", what, uint32(hr))
}

// comInit 在线程上初始化 COM；已经用别的模式初始化过也算可用，不算失败。
func comInit() error {
	hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
	if hr != 0 && uint32(hr) != rpcChangedMode {
		return fmt.Errorf("COM 初始化失败，HRESULT=0x%08X", uint32(hr))
	}
	return nil
}

// audioMeter 读默认播放设备的峰值音量，用来判断「现在有没有在放声音」。
// 这是判断「人在看视频/听东西但没动键鼠」的唯一可靠信号。
type audioMeter struct {
	enumerator uintptr
	device     uintptr
	meter      uintptr
}

func (m *audioMeter) close() {
	comRelease(m.meter)
	comRelease(m.device)
	comRelease(m.enumerator)
	m.meter, m.device, m.enumerator = 0, 0, 0
}

// open 建立到默认播放设备的音量监听接口。
func (m *audioMeter) open() error {
	m.close()
	clsid := clsidMMDeviceEnumerator
	iidEnum := iidIMMDeviceEnumerator
	var enum uintptr
	hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsid)), 0, clsctxAll,
		uintptr(unsafe.Pointer(&iidEnum)), uintptr(unsafe.Pointer(&enum)))
	if hr != 0 {
		return hresultError("CoCreateInstance(MMDeviceEnumerator)", hr)
	}
	m.enumerator = enum

	var device uintptr
	hr = comCall(enum, 4, 0, 0, uintptr(unsafe.Pointer(&device))) // GetDefaultAudioEndpoint(eRender, eConsole)
	if hr != 0 {
		m.close()
		return hresultError("GetDefaultAudioEndpoint", hr)
	}
	m.device = device

	iidMeter := iidIAudioMeterInformation
	var meter uintptr
	hr = comCall(device, 3, uintptr(unsafe.Pointer(&iidMeter)), clsctxAll, 0, uintptr(unsafe.Pointer(&meter)))
	if hr != 0 {
		m.close()
		return hresultError("Activate(IAudioMeterInformation)", hr)
	}
	m.meter = meter
	return nil
}

// peak 返回当前输出设备的峰值音量。接口失效时先重建再试一次，
// 这样换耳机、切默认设备之后能自己恢复。
func (m *audioMeter) peak() (float32, error) {
	if m.meter == 0 {
		if err := m.open(); err != nil {
			return 0, err
		}
	}
	var v float32
	hr := comCall(m.meter, 3, uintptr(unsafe.Pointer(&v))) // GetPeakValue
	if hr != 0 {
		m.close()
		if err := m.open(); err != nil {
			return 0, err
		}
		hr = comCall(m.meter, 3, uintptr(unsafe.Pointer(&v)))
		if hr != 0 {
			m.close()
			return 0, hresultError("GetPeakValue", hr)
		}
	}
	return v, nil
}
