package main

import (
	"os"
	"path/filepath"
	"strings"
)

// console.addr 记录正在运行的 serve 实例把控制台绑在哪个地址上。
// 第二次双击 yogod.exe 时,新进程靠它找到老实例的控制台并打开,
// 而不是报一句「已经在运行了」然后死掉。

// ensureDir 先把数据目录建出来：首次运行时它可能还不存在。
func ensureDir(dir string) {
	_ = os.MkdirAll(dir, 0o755)
}

func consoleAddrFile(dir string) string {
	return filepath.Join(dir, "console.addr")
}

// readConsoleAddr 读运行中实例的控制台地址;读不到就退回默认地址。
func readConsoleAddr(dir string) string {
	b, err := os.ReadFile(consoleAddrFile(dir))
	if s := strings.TrimSpace(string(b)); err == nil && s != "" {
		return s
	}
	return defaultAddr()
}

// writeConsoleAddr 在 serve 成功绑定端口后落盘。
func writeConsoleAddr(dir, addr string) {
	ensureDir(dir)
	_ = os.WriteFile(consoleAddrFile(dir), []byte(addr), 0o644)
}
