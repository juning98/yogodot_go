//go:build windows

package sys

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

/* ══════════ 只认键盘的"正在打字"检测 ══════════

   GetLastInputInfo 分不清键盘和鼠标 —— 鼠标划一下也算"最近有输入"。
   用它做打字让路，结果就是：只要在动鼠标，键盘灯就一直不更新。
   原版 kbd.py 里其实写清楚了这件事（见 recent_key_input 的注释）：
   正常走键盘钩子，Getter 只是钩子没装时的兜底。Go 版一开始只搬了兜底那条，
   于是"鼠标一动就卡住"。

   这里补上真正的做法：装一个 WH_KEYBOARD_LL 底层钩子，
   只在**真的按下键**时记一个时间戳。 */

var (
	procSetWindowsHookExW   = moduser32.NewProc("SetWindowsHookExW")
	procCallNextHookEx      = moduser32.NewProc("CallNextHookEx")
	procUnhookWindowsHookEx = moduser32.NewProc("UnhookWindowsHookEx")
)

const (
	whKeyboardLL = 13
	hcAction     = 0
	wmKeyDown    = 0x0100
	wmSysKeyDown = 0x0104
)

var (
	// lastKeyAt 是最近一次真实按键的 UnixNano；0 表示装好钩子后还没按过。
	lastKeyAt atomic.Int64
	keyHookOK atomic.Bool
	// keyHookProc 必须一直可达，否则 GC 之后回调就飞了。
	keyHookProc uintptr
)

// keyboardProc 是钩子回调。它运行在装钩子的那个线程上，必须极快 ——
// 慢一点会拖累全系统的键盘响应。
func keyboardProc(code, wparam, lparam uintptr) uintptr {
	if code == hcAction && (wparam == wmKeyDown || wparam == wmSysKeyDown) {
		lastKeyAt.Store(time.Now().UnixNano())
	}
	r, _, _ := procCallNextHookEx.Call(0, code, wparam, lparam)
	return r
}

// StartKeyHook 装上全局键盘钩子。失败不算错误 —— 调用方会退回 GetLastInputInfo。
//
// 钩子随进程退出自动消失，这里不做拆卸：本程序的寿命就是用户开着托盘的时间。
func StartKeyHook() error {
	if keyHookOK.Load() {
		return nil
	}
	done := make(chan error, 1)

	go func() {
		// 底层钩子的回调投递到装它的线程，而这个线程必须自己泵消息
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		keyHookProc = syscall.NewCallback(keyboardProc)
		hinst, _, _ := procGetModuleHandleW.Call(0)
		h, _, err := procSetWindowsHookExW.Call(
			whKeyboardLL, keyHookProc, hinst, 0)
		if h == 0 {
			done <- fmt.Errorf("sys: 键盘钩子安装失败: %v", err)
			return
		}
		keyHookOK.Store(true)
		done <- nil

		for {
			var msg msgStruct
			r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(r) <= 0 {
				return
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
		}
	}()

	return <-done
}

// RecentKeyInput 报告最近 ms 毫秒内有没有**按键盘**。
//
// 钩子没装成功时退回 GetLastInputInfo（那种情况下鼠标也算，但总比完全没有强）。
// 钩子装好了但还没按过任何键，返回 false —— 这正是与"鼠标一动就算打字"的区别。
func RecentKeyInput(ms int) bool {
	if !keyHookOK.Load() {
		return RecentInput(ms)
	}
	at := lastKeyAt.Load()
	if at == 0 {
		return false
	}
	return time.Since(time.Unix(0, at)) < time.Duration(ms)*time.Millisecond
}
