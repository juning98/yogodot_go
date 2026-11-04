//go:build windows

package sys

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

var (
	procCreateMutexW  = modk32.NewProc("CreateMutexW")
	procShellExecuteW = procshell32.NewProc("ShellExecuteW")
)

const errorAlreadyExists = 183

// Instance 是单实例锁。
//
// 两个实例会抢同一把键盘、各开一个托盘图标、互相打断 —— 必须挡住。
type Instance struct {
	handle   syscall.Handle
	acquired bool
}

// AcquireSingleInstance 拿一个命名互斥体。
//
// 返回的 Instance 无论 acquired 与否都可以安全 Close。
func AcquireSingleInstance(name string) (*Instance, error) {
	n, err := syscall.UTF16PtrFromString(`Global\` + name)
	if err != nil {
		return nil, err
	}
	// 这里**绝对不能**事后再单独调一次 GetLastError：
	// last-error 是线程局部的，而两次 syscall 之间 goroutine 可能被
	// 调度到另一个 OS 线程上，读到的是那个线程的旧值（通常是 0）。
	// 结果就是第二个实例也报告"我拿到锁了"—— 单实例保护形同虚设。
	//
	// Proc.Call 的第三个返回值是内核在返回瞬间抓的 GetLastError，
	// 用它才靠得住。
	h, _, callErr := procCreateMutexW.Call(0, 1, uintptr(unsafe.Pointer(n)))
	if h == 0 {
		return nil, fmt.Errorf("sys: CreateMutex 失败: %w", callErr)
	}
	inst := &Instance{handle: syscall.Handle(h)}
	// 名字已存在 = 已经有实例在跑。只有 183 算"已存在"，其余都当拿到。
	inst.acquired = !errors.Is(callErr, syscall.Errno(errorAlreadyExists))
	return inst, nil
}

// Acquired 报告是否拿到了锁（false 表示已有一个实例在跑）。
func (i *Instance) Acquired() bool { return i != nil && i.acquired }

// Close 释放锁。
func (i *Instance) Close() {
	if i != nil && i.handle != 0 {
		syscall.CloseHandle(i.handle)
		i.handle = 0
	}
}

// OpenBrowser 用系统默认浏览器打开一个 URL。
func OpenBrowser(url string) error {
	op, err := syscall.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	u, err := syscall.UTF16PtrFromString(url)
	if err != nil {
		return err
	}
	r, _, callErr := procShellExecuteW.Call(0,
		uintptr(unsafe.Pointer(op)), uintptr(unsafe.Pointer(u)), 0, 0, 1 /* SW_SHOWNORMAL */)
	if r <= 32 {
		return fmt.Errorf("sys: ShellExecute 失败: %w", callErr)
	}
	return nil
}
