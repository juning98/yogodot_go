package main

import (
	"os"
	"syscall"
)

// yogod 以 windowsgui 子系统构建：双击启动时不弹黑窗。
// 代价是从终端里敲子命令（probe/status/theme 这些）时,进程天生没有控制台,
// fmt.Print 系列全写进虚空。attachConsole 把输出接回父终端补上这条路。
//
// 注意：cmd/PowerShell 不会等 GUI 子系统程序退出,所以输出会出现在
// 下一个提示符后面 —— 脚本报表要等结果时用 `start /wait yogod.exe ...`。

var procAttachConsole = syscall.NewLazyDLL("kernel32.dll").NewProc("AttachConsole")

// attachParentProcess 是 (DWORD)-1：挂到启动我们的那个终端。
const attachParentProcess = ^uintptr(0)

// attachConsole 尝试挂到父控制台并重接 stdout/stderr。
// 双击启动（没有父终端）或 hook 这类静默子命令不该调用它。
// 返回是否接上;没接上时打印依然是安全的,只是丢进虚空。
//
// 两种父终端行为不一样：
//   - 经典 conhost：GUI 子进程没控制台，AttachConsole 成功，用 CONOUT$ 输出
//   - ConPTY 终端（Windows Terminal / 各类集成终端）：父控制台是伪终端，
//     AttachConsole 会失败 —— 这种环境 stdout 接不回去，调用方要有文件兜底
func attachConsole() bool {
	r, _, err := procAttachConsole.Call(attachParentProcess)
	if r == 0 {
		// 有的宿主会直接给 GUI 子进程挂好控制台，这时失败码是
		// ERROR_ACCESS_DENIED 且 GetStdHandle 是有效句柄 —— 继续走重接。
		if err != syscall.ERROR_ACCESS_DENIED {
			return false
		}
	}
	// 父进程给了现成句柄（管道 / 文件重定向）就用它;
	// 没有（GUI 程序的常态）才开 CONOUT$ —— 挂上控制台之后,
	// GetStdHandle 返回的还是进程创建时的旧句柄,CONOUT$ 才是新控制台。
	if h, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE); h == 0 || h == syscall.InvalidHandle {
		f, ferr := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
		if ferr != nil {
			return false
		}
		os.Stdout = f
		os.Stderr = f
	}
	return true
}
