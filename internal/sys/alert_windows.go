//go:build windows

package sys

import (
	"syscall"
	"unsafe"
)

var procMessageBoxW = moduser32.NewProc("MessageBoxW")

const mbIconError = 0x10

// Alert 弹一个系统错误框。
//
// windowsgui 程序启动失败（比如端口被占）时没有终端可打印,
// 静默退出会让用户以为「双击没反应」—— 这是最糟的失败方式。
// 所以只在没有控制台可说的路径上用它,别拿来报普通错误。
func Alert(title, text string) {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(text)
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), mbIconError)
}
