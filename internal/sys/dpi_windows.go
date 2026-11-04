//go:build windows

package sys

import "syscall"

var (
	modShcore = syscall.NewLazyDLL("shcore.dll")

	procSetProcessDpiAwarenessContext = moduser32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDpiAwareness        = modShcore.NewProc("SetProcessDpiAwareness")
	procSetProcessDPIAware            = moduser32.NewProc("SetProcessDPIAware")
)

// dpiAwarenessContextPerMonitorAwareV2 是 (DPI_AWARENESS_CONTEXT)-4,
// Win10 1703 起支持:每块显示器各按各的 DPI 渲染,菜单文字不再糊。
const dpiAwarenessContextPerMonitorAwareV2 = ^uintptr(3)

// processPerMonitorDpiAware 是 PROCESS_PER_MONITOR_DPI_AWARE(shcore 的 V1 接口)。
const processPerMonitorDpiAware = 2

// MakeDPIAware 声明进程按高分屏原生分辨率渲染。
//
// 不声明的话 Windows 把进程当 96 DPI 程序,托盘菜单先离屏画成位图再放大,
// 字体就是糊的 —— 这是「托盘菜单字体模糊」的根因。
// 必须在创建任何窗口之前调用(托盘 Run 的开头),进程里只调一次。
func MakeDPIAware() {
	// 新接口优先;老系统(Vista~Win8.1)一路降级,总有一个能成
	if r, _, _ := procSetProcessDpiAwarenessContext.Call(dpiAwarenessContextPerMonitorAwareV2); r != 0 {
		return
	}
	if r, _, _ := procSetProcessDpiAwareness.Call(processPerMonitorDpiAware); r == 0 { // S_OK
		return
	}
	procSetProcessDPIAware.Call()
}
