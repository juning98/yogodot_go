package main

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"
)

// yogoICO 是嵌进二进制的托盘图标（原版的 yogo.ico）。
// 嵌入式之后,新机器上第一次跑也有 logo,不用手动往数据目录放文件。
//
//go:embed assets/yogo.ico
var yogoICO []byte

// ensureIcon 把内嵌图标落到数据目录并返回路径,托盘用它当 logo。
// 已经存在且内容一致就不重写;写不进去就返回空串,托盘退回系统默认图标。
func ensureIcon(dir string) string {
	p := filepath.Join(dir, "yogod.ico")
	if b, err := os.ReadFile(p); err == nil && bytes.Equal(b, yogoICO) {
		return p
	}
	if err := os.WriteFile(p, yogoICO, 0o644); err != nil {
		return ""
	}
	return p
}
