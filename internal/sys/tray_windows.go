//go:build windows

package sys

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"syscall"
	"unsafe"
)

var (
	procRegisterClassExW       = moduser32.NewProc("RegisterClassExW")
	procCreateWindowExW        = moduser32.NewProc("CreateWindowExW")
	procDefWindowProcW         = moduser32.NewProc("DefWindowProcW")
	procRegisterWindowMessageW = moduser32.NewProc("RegisterWindowMessageW")
	procCreatePopupMenu        = moduser32.NewProc("CreatePopupMenu")
	procAppendMenuW            = moduser32.NewProc("AppendMenuW")
	procTrackPopupMenu         = moduser32.NewProc("TrackPopupMenu")
	procDestroyMenu            = moduser32.NewProc("DestroyMenu")
	procGetCursorPos           = moduser32.NewProc("GetCursorPos")
	procSetForegroundWindow    = moduser32.NewProc("SetForegroundWindow")
	procPostMessageW           = moduser32.NewProc("PostMessageW")
	procShowWindow             = moduser32.NewProc("ShowWindow")
	procGetMessageW            = moduser32.NewProc("GetMessageW")
	procTranslateMessage       = moduser32.NewProc("TranslateMessage")
	procDispatchMessageW       = moduser32.NewProc("DispatchMessageW")
	procPostQuitMessage        = moduser32.NewProc("PostQuitMessage")
	procLoadImageW             = moduser32.NewProc("LoadImageW")
	procDestroyWindow          = moduser32.NewProc("DestroyWindow")
	procshell32                = syscall.NewLazyDLL("shell32.dll")
	procShellNotifyIconW       = procshell32.NewProc("Shell_NotifyIconW")
	procGetModuleHandleW       = modk32.NewProc("GetModuleHandleW")
)

const (
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmCommand       = 0x0111
	wmQuit          = 0x0012
	wmAppTray       = 0x0400 + 17
	wmLButtonUp     = 0x0202
	wmRButtonUp     = 0x0205
	wmLButtonDblClk = 0x0203

	nimAdd    = 0
	nimModify = 1
	nimDelete = 2

	nifMessage = 0x1
	nifIcon    = 0x2
	nifTip     = 0x4

	imageIcon      = 1
	lrLoadFromFile = 0x0010
	lrDefaultSize  = 0x0040

	tpmRightAlign  = 0x0008
	tpmBottomAlign = 0x0020
	tpmReturnCmd   = 0x0100

	mfString    = 0x0000
	mfSeparator = 0x0800
	mfChecked   = 0x0008
	mfGrayed    = 0x0001

	swHide = 0

	// errorClassAlreadyExists 是 RegisterClassEx 的"类已注册"，重复启动时会碰到。
	errorClassAlreadyExists = 1410
)

type notifyIconData struct {
	CbSize           uint32
	HWnd             syscall.Handle
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            syscall.Handle
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         [16]byte
	HBalloonIcon     syscall.Handle
}

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     syscall.Handle
	HIcon         syscall.Handle
	HCursor       syscall.Handle
	HbrBackground syscall.Handle
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       syscall.Handle
}

type point struct{ X, Y int32 }

// MenuItem 是托盘右键菜单的一项。
type MenuItem struct {
	ID        int
	Label     string
	Checked   bool
	Disabled  bool
	Separator bool
}

// Tray 是一个托盘图标 + 右键菜单。
//
// Run 会占用调用线程的消息循环，所以必须在一个 LockOSThread 的 goroutine 里跑。
type Tray struct {
	Tip      string
	IconPath string
	// Items 每次弹菜单时调用，这样菜单项能反映实时状态。
	Items func() []MenuItem
	// OnMenu 点菜单项回调。
	OnMenu func(id int)
	// OnClick 左键单击/双击回调。
	OnClick func()
	// OnExit 消息循环结束时回调。
	OnExit func()

	// hwnd 用 atomic：窗口在托盘 goroutine 的消息循环里建，
	// 但 Quit 是别的 goroutine 调的（ctx 取消 / 主线程退出）。
	hwnd              atomic.Uintptr
	nid               notifyIconData
	taskbarCreatedMsg uint32
	wndProcRef        uintptr // 必须持住，否则 GC 回收后窗口过程就飞了
	classRegistered   bool
}

// Run 建图标并进入消息循环，直到 WM_QUIT 或 ctx 取消。
func (t *Tray) Run(ctx context.Context) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// 建任何窗口之前先声明 DPI 感知,不然托盘菜单文字被位图拉伸发糊
	MakeDPIAware()

	if err := t.create(); err != nil {
		return err
	}
	defer t.remove()

	for {
		var msg msgStruct
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 { // 0 = WM_QUIT，-1 = 错误
			if t.OnExit != nil {
				t.OnExit()
			}
			return nil
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

type msgStruct struct {
	HWnd    syscall.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}

// Quit 让消息循环退出。
func (t *Tray) Quit() {
	if h := t.hwnd.Load(); h != 0 {
		procPostMessageW.Call(h, wmClose, 0, 0)
	}
}

func (t *Tray) create() error {
	hinst, _, _ := procGetModuleHandleW.Call(0)

	className, _ := syscall.UTF16PtrFromString("yogodTrayWnd")
	windowName, _ := syscall.UTF16PtrFromString("yogod")

	// 回调必须是包级可达的 uintptr，否则 GC 会把它收走
	t.wndProcRef = syscall.NewCallback(t.wndProc)

	wc := wndClassEx{
		LpfnWndProc:   t.wndProcRef,
		HInstance:     syscall.Handle(hinst),
		LpszClassName: className,
	}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	// 必须用 RegisterClassExW 配 WNDCLASSEX。
	// 用 RegisterClassW 去喂 WNDCLASSEX 会整体错位一个 cbSize ——
	// lpfnWndProc 读到的是 style、lpszClassName 读到的是别的指针，
	// 类注册不上，随后 CreateWindowEx 报 "Cannot find window class"。
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		// 1410 = ERROR_CLASS_ALREADY_EXISTS，重复启动时正常
		if !errors.Is(err, syscall.Errno(errorClassAlreadyExists)) {
			return fmt.Errorf("%w: RegisterClassEx 失败: %v", ErrNoTray, err)
		}
	}
	t.classRegistered = true

	hwnd, _, err := procCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(windowName)),
		0, 0, 0, 0, 0, 0, 0, hinst, 0)
	if hwnd == 0 {
		return fmt.Errorf("%w: CreateWindowEx 失败: %v", ErrNoTray, err)
	}
	t.hwnd.Store(uintptr(hwnd))

	// Explorer 重启后会广播 TaskbarCreated，收到要重新加图标
	// —— 旧版没做这件事，结果 Explorer 一崩图标就永久消失。
	msgName, _ := syscall.UTF16PtrFromString("TaskbarCreated")
	tb, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(msgName)))
	t.taskbarCreatedMsg = uint32(tb)

	var hicon syscall.Handle
	if t.IconPath != "" {
		p, err := syscall.UTF16PtrFromString(t.IconPath)
		if err == nil {
			h, _, _ := procLoadImageW.Call(0, uintptr(unsafe.Pointer(p)),
				imageIcon, 0, 0, lrLoadFromFile|lrDefaultSize)
			hicon = syscall.Handle(h)
		}
	}

	t.nid = notifyIconData{
		HWnd:             syscall.Handle(t.hwnd.Load()),
		UID:              1,
		UFlags:           nifMessage | nifIcon | nifTip,
		UCallbackMessage: wmAppTray,
		HIcon:            hicon,
	}
	t.nid.CbSize = uint32(unsafe.Sizeof(t.nid))
	copyTip(t.nid.SzTip[:], t.Tip)

	if !t.addIcon() {
		return fmt.Errorf("%w: Shell_NotifyIcon 失败", ErrNoTray)
	}
	return nil
}

func (t *Tray) addIcon() bool {
	r, _, _ := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&t.nid)))
	return r != 0
}

func (t *Tray) remove() {
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&t.nid)))
	if h := t.hwnd.Swap(0); h != 0 {
		procDestroyWindow.Call(h)
	}
}

func copyTip(dst []uint16, s string) {
	u, err := syscall.UTF16FromString(s)
	if err != nil {
		return
	}
	if len(u) > len(dst) {
		u = u[:len(dst)]
	}
	copy(dst, u)
}

func (t *Tray) wndProc(hwnd syscall.Handle, msg uint32, wparam, lparam uintptr) uintptr {
	switch {
	case t.taskbarCreatedMsg != 0 && msg == t.taskbarCreatedMsg:
		// Explorer 重启：把图标加回去
		t.addIcon()
		return 0

	case msg == wmAppTray:
		switch low := uint32(lparam) & 0xFFFF; low {
		case wmRButtonUp:
			t.showMenu()
		case wmLButtonUp, wmLButtonDblClk:
			if t.OnClick != nil {
				go t.OnClick()
			}
		}
		return 0

	case msg == wmCommand:
		if t.OnMenu != nil {
			go t.OnMenu(int(wparam & 0xFFFF))
		}
		return 0

	case msg == wmClose, msg == wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wparam, lparam)
	return r
}

func (t *Tray) showMenu() {
	var items []MenuItem
	if t.Items != nil {
		items = t.Items()
	}
	if len(items) == 0 {
		return
	}

	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	for _, it := range items {
		label, _ := syscall.UTF16PtrFromString(it.Label)
		if it.Separator {
			procAppendMenuW.Call(menu, mfSeparator, 0, 0)
			continue
		}
		flags := uintptr(mfString)
		if it.Checked {
			flags |= mfChecked
		}
		if it.Disabled {
			flags |= mfGrayed
		}
		procAppendMenuW.Call(menu, flags, uintptr(it.ID), uintptr(unsafe.Pointer(label)))
	}

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// TrackPopupMenu 的老规矩：不先 SetForegroundWindow，菜单点外面不会消失
	h := t.hwnd.Load()
	procSetForegroundWindow.Call(h)
	cmd, _, _ := procTrackPopupMenu.Call(menu,
		tpmRightAlign|tpmBottomAlign|tpmReturnCmd,
		uintptr(pt.X), uintptr(pt.Y), 0, h, 0)
	procPostMessageW.Call(h, 0, 0, 0)

	if cmd != 0 && t.OnMenu != nil {
		go t.OnMenu(int(cmd))
	}
}
