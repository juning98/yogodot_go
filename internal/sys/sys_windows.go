//go:build windows

// Package sys 封装程序需要的 Windows 系统状态：输入空闲、锁屏、打字检测。
//
// 旧版把这三样直接写在 kbd.py 里，和 HID 逻辑混在一起。
// 拆出来之后引擎可以独立测试，也不再有"跨模块直接写全局变量"的问题。
package sys

import (
	"syscall"
	"time"
	"unsafe"
)

var (
	moduser32 = syscall.NewLazyDLL("user32.dll")
	modk32    = syscall.NewLazyDLL("kernel32.dll")

	procGetLastInputInfo = moduser32.NewProc("GetLastInputInfo")
	procOpenInputDesktop = moduser32.NewProc("OpenInputDesktop")
	procCloseDesktop     = moduser32.NewProc("CloseDesktop")
	procGetTickCount64   = modk32.NewProc("GetTickCount64")
)

type lastInputInfo struct {
	CBSize uint32
	DwTime uint32
}

const desktopSwitchDesktop = 0x0100

// TypingGuardMS 是"最近这段时间内算正在打字"的窗口。
const TypingGuardMS = 250

// IdleSeconds 返回距离上次键盘/鼠标输入过了多少秒。
//
// GetLastInputInfo 给的是 32 位 tick，会回绕，所以要自己按 32 位减法处理。
// 查不到就当成"已经离开"——宁可多熄一次，也别因为一次查询失败整夜亮着。
func IdleSeconds(fallback float64) float64 {
	var li lastInputInfo
	li.CBSize = uint32(unsafe.Sizeof(li))
	if r, _, _ := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&li))); r == 0 {
		return fallback
	}
	now, _, _ := procGetTickCount64.Call()
	d := (uint32(now) - li.DwTime) & 0xFFFFFFFF
	return float64(d) / 1000.0
}

// RecentInput 报告最近 ms 毫秒内有没有键鼠输入（鼠标移动也算）。
func RecentInput(ms int) bool {
	var li lastInputInfo
	li.CBSize = uint32(unsafe.Sizeof(li))
	if r, _, _ := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&li))); r == 0 {
		return false // 查不到就别妨碍推流
	}
	now, _, _ := procGetTickCount64.Call()
	d := (uint32(now) - li.DwTime) & 0xFFFFFFFF
	return d < uint32(ms)
}

// Locked 报告 Windows 是不是锁屏了。
//
// 锁屏时输入桌面切到 Winlogon，普通进程打不开它。
// 注意 UAC 弹窗的安全桌面也会让这个调用失败，所以调用方要连续多次命中才当真
// —— 见 engine 里的去抖。
func Locked() bool {
	h, _, _ := procOpenInputDesktop.Call(0, 0, desktopSwitchDesktop)
	if h == 0 {
		return true
	}
	procCloseDesktop.Call(h)
	return false
}

// Now 是一个便于测试的时钟入口。
func Now() time.Time { return time.Now() }
