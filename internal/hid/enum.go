//go:build windows

// Package hid 是 Windows 上的裸 HID 传输层。
//
// 只用 setupapi / hid / kernel32 三个系统 DLL，不依赖 cgo、不依赖第三方库。
// 这一层只负责"打开一个口、读、写、关"，不懂协议 —— 协议在 internal/device。
package hid

import (
	"fmt"
	"syscall"
	"unsafe"
)

// 设备厂商 ID。ATK YOGO 75 PRO 及其接收器都用这个。
const VID = 0x373B

// 厂商通道的 usage page / usage。键盘直连与 2.4G 接收器都会各出一个。
const (
	UsagePageVendor = 0xFF60
	UsageVendor     = 0x61
	// VIA 通道（自定义灯效缓冲区）的接口。
	UsagePageVIA = 0xFF60
	UsageVIA     = 0x61
	// VIA 的 boot 接口是独立的 usage page / usage。
	viaPage = 0xFF60
)

var (
	modsetupapi = syscall.NewLazyDLL("setupapi.dll")
	modhid      = syscall.NewLazyDLL("hid.dll")
	modkernel32 = syscall.NewLazyDLL("kernel32")
)

var (
	procSetupDiGetClassDevsW             = modsetupapi.NewProc("SetupDiGetClassDevsW")
	procSetupDiEnumDeviceInterfaces      = modsetupapi.NewProc("SetupDiEnumDeviceInterfaces")
	procSetupDiGetDeviceInterfaceDetailW = modsetupapi.NewProc("SetupDiGetDeviceInterfaceDetailW")
	procSetupDiDestroyDeviceInfoList     = modsetupapi.NewProc("SetupDiDestroyDeviceInfoList")
	procHidDGetHidGuid                   = modhid.NewProc("HidD_GetHidGuid")
	procHidDGetAttributes                = modhid.NewProc("HidD_GetAttributes")
	procHidDGetPreparsedData             = modhid.NewProc("HidD_GetPreparsedData")
	procHidDFreePreparsedData            = modhid.NewProc("HidD_FreePreparsedData")
	procHidPGetCaps                      = modhid.NewProc("HidP_GetCaps")
	procHidDSetNumInputBuffers           = modhid.NewProc("HidD_SetNumInputBuffers")
	procHidDGetProductString             = modhid.NewProc("HidD_GetProductString")
	procCreateFileW                      = modkernel32.NewProc("CreateFileW")
	procCancelIoEx                       = modkernel32.NewProc("CancelIoEx")
)

const (
	digcfPresent         = 0x02
	digcfDeviceInterface = 0x10

	genericRead        = 0x80000000
	genericWrite       = 0x40000000
	fileShareRead      = 1
	fileShareWrite     = 2
	openExisting       = 3
	fileFlagOverlapped = 0x40000000
)

// GUID 对应 Windows 的 GUID 结构。
type GUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

type spDeviceInterfaceData struct {
	CBSize             uint32
	InterfaceClassGUID GUID
	Flags              uint32
	Reserved           uintptr
}

type spDevInfoData struct {
	CBSize    uint32
	ClassGUID GUID
	DevInst   uint32
	Reserved  uintptr
}

type hidAttributes struct {
	Size          uint32
	VendorID      uint16
	ProductID     uint16
	VersionNumber uint16
}

type hidpCaps struct {
	Usage                     uint16
	UsagePage                 uint16
	InputReportByteLength     uint16
	OutputReportByteLength    uint16
	FeatureReportByteLength   uint16
	Reserved                  [17]uint16
	NumberLinkCollectionNodes uint16
	NumberInputButtonCaps     uint16
	NumberInputValueCaps      uint16
	NumberInputDataIndices    uint16
	NumberOutputButtonCaps    uint16
	NumberOutputValueCaps     uint16
	NumberOutputDataIndices   uint16
	NumberFeatureButtonCaps   uint16
	NumberFeatureValueCaps    uint16
	NumberFeatureDataIndices  uint16
}

// Interface 描述一个枚举到的 HID 接口。
type Interface struct {
	// Path 是可直接喂给 CreateFile 的设备路径。
	Path string
	// VID / PID 是厂商与产品 ID。
	VID, PID uint16
	// UsagePage / Usage 决定这个口是干什么的。
	UsagePage, Usage uint16
	// RxLen / TxLen 是设备报告的长度（含 report id 字节）。
	// 写的时候必须按 TxLen 补齐，否则 WriteFile 会失败。
	RxLen, TxLen int
	// Product 是产品名。接收器报的名字里带 "Dongle"，键盘直连不带 ——
	// 这是判断"走的是 2.4G 还是数据线"的依据。
	Product string
}

// IsDongle 报告这个口是不是 2.4G 接收器。
func (i Interface) IsDongle() bool {
	return containsFold(i.Product, "dongle")
}

// IsVendor 报告这是不是厂商通信口（FF60/61）。
func (i Interface) IsVendor() bool {
	return i.UsagePage == UsagePageVendor && i.Usage == UsageVendor
}

func containsFold(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			a, b := s[i+j], sub[j]
			if 'A' <= a && a <= 'Z' {
				a += 'a' - 'A'
			}
			if 'A' <= b && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// Enumerate 列出系统里所有 HID 接口。
func Enumerate() ([]Interface, error) {
	var hidGUID GUID
	procHidDGetHidGuid.Call(uintptr(unsafe.Pointer(&hidGUID)))

	h, _, err := procSetupDiGetClassDevsW.Call(
		uintptr(unsafe.Pointer(&hidGUID)), 0, 0,
		digcfPresent|digcfDeviceInterface)
	if h == uintptr(syscall.InvalidHandle) {
		return nil, fmt.Errorf("hid: SetupDiGetClassDevs 失败: %w", err)
	}
	defer procSetupDiDestroyDeviceInfoList.Call(h)

	var out []Interface
	for index := uint32(0); ; index++ {
		var did spDeviceInterfaceData
		did.CBSize = uint32(unsafe.Sizeof(did))
		r, _, _ := procSetupDiEnumDeviceInterfaces.Call(
			h, 0, uintptr(unsafe.Pointer(&hidGUID)), uintptr(index),
			uintptr(unsafe.Pointer(&did)))
		if r == 0 {
			break // ERROR_NO_MORE_ITEMS
		}
		path, ok := interfaceDetail(h, &did)
		if !ok {
			continue
		}
		it, ok := describe(path)
		if !ok {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

// interfaceDetail 取出设备接口的路径。
//
// SP_DEVICE_INTERFACE_DETAIL_DATA_W 的 cbSize 在 32/64 位下不同（6 / 8），
// 这里按指针宽度算，别写死。
func interfaceDetail(set uintptr, did *spDeviceInterfaceData) (string, bool) {
	var detailSize uint32 = 4 + 2
	if unsafe.Sizeof(uintptr(0)) == 8 {
		detailSize = 8
	}
	// 先问需要多大
	var required uint32
	procSetupDiGetDeviceInterfaceDetailW.Call(
		set, uintptr(unsafe.Pointer(did)), 0, 0,
		uintptr(unsafe.Pointer(&required)), 0)
	if required == 0 {
		return "", false
	}

	buf := make([]byte, required)
	*(*uint32)(unsafe.Pointer(&buf[0])) = detailSize
	r, _, _ := procSetupDiGetDeviceInterfaceDetailW.Call(
		set, uintptr(unsafe.Pointer(did)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(required),
		uintptr(unsafe.Pointer(&required)), 0)
	if r == 0 {
		return "", false
	}
	if len(buf) < 6 {
		return "", false
	}
	// 路径从偏移 4 开始，UTF-16LE 且以 NUL 结尾。
	// 容量必须贴着 buf 的真实长度：拍一个 [4096] 的假容量
	// 在 -race 下会被 checkptr 判成 "straddles multiple allocations"。
	u16s := unsafe.Slice((*uint16)(unsafe.Pointer(&buf[4])), (len(buf)-4)/2)
	return syscall.UTF16ToString(u16s), true
}

// describe 打开一个口读出属性，然后立刻关掉。
// 打不开的接口（被独占、权限不足）会被跳过 —— 枚举阶段不该因为一个口失败就全盘失败。
func describe(path string) (Interface, bool) {
	var it Interface
	it.Path = path

	h, err := openHandle(path, 0) // 属性查询不需要读写权限
	if err != nil {
		return it, false
	}
	defer syscall.CloseHandle(h)

	var attr hidAttributes
	attr.Size = uint32(unsafe.Sizeof(attr))
	if r, _, _ := procHidDGetAttributes.Call(uintptr(h),
		uintptr(unsafe.Pointer(&attr))); r == 0 {
		return it, false
	}
	it.VID, it.PID = attr.VendorID, attr.ProductID

	var pp unsafe.Pointer
	if r, _, _ := procHidDGetPreparsedData.Call(uintptr(h),
		uintptr(unsafe.Pointer(&pp))); r == 0 {
		return it, false
	}
	defer procHidDFreePreparsedData.Call(uintptr(pp))

	var caps hidpCaps
	if r, _, _ := procHidPGetCaps.Call(uintptr(pp),
		uintptr(unsafe.Pointer(&caps))); r == 0 {
		return it, false
	}
	it.UsagePage, it.Usage = caps.UsagePage, caps.Usage
	it.RxLen = int(caps.InputReportByteLength)
	it.TxLen = int(caps.OutputReportByteLength)

	// 产品名：用来分 2.4G 接收器 / 有线直连
	var nameBuf [128]uint16
	if r, _, _ := procHidDGetProductString.Call(uintptr(h),
		uintptr(unsafe.Pointer(&nameBuf[0])), 256); r != 0 {
		it.Product = syscall.UTF16ToString(nameBuf[:])
	}
	return it, true
}

// VendorInterfaces 返回所有 ATK 厂商通道口（FF60/61）。
//
// 插着数据线时会出现两个长得一模一样的口，但只有一个后面真的挂着键盘 ——
// 调用方必须逐个握手，谁回包用谁，不能拿第一个。
func VendorInterfaces() ([]Interface, error) {
	all, err := Enumerate()
	if err != nil {
		return nil, err
	}
	var out []Interface
	for _, it := range all {
		if it.VID == VID && it.IsVendor() {
			out = append(out, it)
		}
	}
	return out, nil
}

// openHandle 打开一个设备路径。
//
// access 为 0 时只查属性；读写要传 genericRead|genericWrite。
func openHandle(path string, access uint32) (syscall.Handle, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return syscall.InvalidHandle, err
	}
	r, _, callErr := procCreateFileW.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(access),
		uintptr(fileShareRead|fileShareWrite),
		0,
		uintptr(openExisting),
		uintptr(fileFlagOverlapped),
		0)
	h := syscall.Handle(r)
	if h == syscall.InvalidHandle {
		return h, fmt.Errorf("hid: 打开 %s 失败: %w", path, callErr)
	}
	return h, nil
}
