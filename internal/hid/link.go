//go:build windows

package hid

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

var (
	procCreateEventW        = modkernel32.NewProc("CreateEventW")
	procResetEvent          = modkernel32.NewProc("ResetEvent")
	procWaitForSingleObject = modkernel32.NewProc("WaitForSingleObject")
	procGetOverlappedResult = modkernel32.NewProc("GetOverlappedResult")
	procReadFile            = modkernel32.NewProc("ReadFile")
	procWriteFile           = modkernel32.NewProc("WriteFile")
)

const (
	waitObject0                  = 0x00000000
	waitTimeout                  = 0x00000102
	errorIOPending syscall.Errno = 997
)

// ErrTimeout 表示一次读写没能在超时内完成。
var ErrTimeout = errors.New("hid: 操作超时")

// ErrClosed 表示连接已经关了。
var ErrClosed = errors.New("hid: 连接已关闭")

// Link 是一条已打开的 HID 连接。
//
// 所有读写都走 overlapped IO + 独立事件，所以能可靠地做超时 ——
// 旧版用 ctypes 手写的 WaitForSingleObject/CancelIoEx 就是为了这件事。
//
// Link 的 Read 只能由一个 goroutine 调用（读线程），Write 可以并发（内部加锁）。
type Link struct {
	path         string
	h            syscall.Handle
	rxLen, txLen int
	evRead       syscall.Handle
	evWrite      syscall.Handle
	writeMu      sync.Mutex
	closed       atomic.Bool
}

// Open 打开一个 HID 接口并准备好读写。
func Open(path string, rxLen, txLen int) (*Link, error) {
	if txLen <= 0 {
		txLen = 33
	}
	if rxLen <= 0 {
		rxLen = 33
	}
	h, err := openHandle(path, genericRead|genericWrite)
	if err != nil {
		return nil, err
	}
	// 64 个输入缓冲，避免高速推流时回包被丢
	procHidDSetNumInputBuffers.Call(uintptr(h), 64)

	l := &Link{path: path, h: h, rxLen: rxLen, txLen: txLen}
	if l.evRead, err = createEvent(); err != nil {
		syscall.CloseHandle(h)
		return nil, err
	}
	if l.evWrite, err = createEvent(); err != nil {
		syscall.CloseHandle(l.evRead)
		syscall.CloseHandle(h)
		return nil, err
	}
	return l, nil
}

// Path 返回设备路径。
func (l *Link) Path() string { return l.path }

// TxLen 返回设备的输出报告长度。
func (l *Link) TxLen() int { return l.txLen }

// Closed 报告连接是否已关。
func (l *Link) Closed() bool { return l.closed.Load() }

func createEvent() (syscall.Handle, error) {
	r, _, err := procCreateEventW.Call(0, 1, 0, 0) // manual reset, initially non-signaled
	if r == 0 {
		return syscall.InvalidHandle, fmt.Errorf("hid: CreateEvent 失败: %w", err)
	}
	return syscall.Handle(r), nil
}

// Write 写一包数据。short 的情况会用 0 补齐到设备要求的长度 ——
// HID 输出报告长度是固定的，写不够长度系统直接返回失败。
func (l *Link) Write(ctx context.Context, b []byte) error {
	if l.closed.Load() {
		return ErrClosed
	}
	l.writeMu.Lock()
	defer l.writeMu.Unlock()

	deadline, ok := ctx.Deadline()
	timeout := 800 * time.Millisecond
	if ok {
		timeout = time.Until(deadline)
		if timeout <= 0 {
			return context.DeadlineExceeded
		}
	}

	buf := make([]byte, l.txLen)
	copy(buf, b)

	var ov syscall.Overlapped
	ov.HEvent = l.evWrite
	procResetEvent.Call(uintptr(l.evWrite))

	r, _, callErr := procWriteFile.Call(
		uintptr(l.h),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		0, // overlapped: lpNumberOfBytesWritten 必须为 NULL
		uintptr(unsafe.Pointer(&ov)))
	if r == 0 && callErr != errorIOPending {
		return fmt.Errorf("hid: WriteFile 失败: %w", callErr)
	}

	switch l.wait(uintptr(l.evWrite), timeout) {
	case waitObject0:
		var n uint32
		procGetOverlappedResult.Call(uintptr(l.h),
			uintptr(unsafe.Pointer(&ov)), uintptr(unsafe.Pointer(&n)), 0)
		return nil
	case waitTimeout:
		procCancelIoEx.Call(uintptr(l.h), uintptr(unsafe.Pointer(&ov)))
		return ErrTimeout
	default:
		return fmt.Errorf("hid: 等待写入时出错")
	}
}

// Read 读一包数据，返回实际字节数。超时返回 ErrTimeout。
//
// 只能由一个 goroutine 调用（同一时刻只有一个挂起的读）。
func (l *Link) Read(ctx context.Context, timeout time.Duration) ([]byte, error) {
	if l.closed.Load() {
		return nil, ErrClosed
	}
	if deadline, ok := ctx.Deadline(); ok {
		if d := time.Until(deadline); d < timeout {
			timeout = d
		}
	}
	if timeout <= 0 {
		return nil, context.DeadlineExceeded
	}

	buf := make([]byte, l.rxLen)
	var ov syscall.Overlapped
	ov.HEvent = l.evRead
	procResetEvent.Call(uintptr(l.evRead))

	r, _, callErr := procReadFile.Call(
		uintptr(l.h),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		0,
		uintptr(unsafe.Pointer(&ov)))
	if r == 0 && callErr != errorIOPending {
		return nil, fmt.Errorf("hid: ReadFile 失败: %w", callErr)
	}

	switch l.wait(uintptr(l.evRead), timeout) {
	case waitObject0:
		var n uint32
		procGetOverlappedResult.Call(uintptr(l.h),
			uintptr(unsafe.Pointer(&ov)), uintptr(unsafe.Pointer(&n)), 0)
		if n == 0 {
			return nil, nil
		}
		return buf[:n], nil
	case waitTimeout:
		procCancelIoEx.Call(uintptr(l.h), uintptr(unsafe.Pointer(&ov)))
		var n uint32
		procGetOverlappedResult.Call(uintptr(l.h),
			uintptr(unsafe.Pointer(&ov)), uintptr(unsafe.Pointer(&n)), 1)
		return nil, ErrTimeout
	default:
		return nil, fmt.Errorf("hid: 等待读取时出错")
	}
}

func (l *Link) wait(ev uintptr, d time.Duration) uintptr {
	ms := uintptr(d / time.Millisecond)
	if ms == 0 {
		ms = 1
	}
	r, _, _ := procWaitForSingleObject.Call(ev, ms)
	return r
}

// Close 关闭连接。可重复调用。
func (l *Link) Close() error {
	if l.closed.Swap(true) {
		return nil
	}
	// 叫醒任何挂起的 IO，否则关闭时线程会卡在 WaitForSingleObject 上
	procCancelIoEx.Call(uintptr(l.h), 0)
	if l.evRead != 0 {
		syscall.CloseHandle(l.evRead)
		l.evRead = 0
	}
	if l.evWrite != 0 {
		syscall.CloseHandle(l.evWrite)
		l.evWrite = 0
	}
	return syscall.CloseHandle(l.h)
}

// ProductName 读取设备产品名。失败返回空串。
func (l *Link) ProductName() string {
	var nameBuf [128]uint16
	if r, _, _ := procHidDGetProductString.Call(uintptr(l.h),
		uintptr(unsafe.Pointer(&nameBuf[0])), 256); r == 0 {
		return ""
	}
	return syscall.UTF16ToString(nameBuf[:])
}
