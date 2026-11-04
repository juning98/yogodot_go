//go:build windows

package sys

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
)

// runHidden 跑一个不弹黑框的子进程并等它结束。
//
// 从无控制台的父进程起控制台程序，Windows 会给它开一个黑框 ——
// 所有辅助子进程（powershell 之类）都要走这里。
func runHidden(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return cmd.Run()
}

// RunContext 是给需要 ctx 的子进程用的。
func runHiddenContext(ctx context.Context, name string, args ...string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	return cmd.Run()
}

// ErrNoTray 表示托盘建不起来（不致命，只是没有图标）。
var ErrNoTray = errors.New("sys: 托盘创建失败")
