package api

import (
	"embed"
	"io/fs"
)

//go:embed console.html
var embedded embed.FS

// ConsoleFS 返回打包进二进制的控制台页面。
//
// 页面只做"设置 + 预览"，不参与出帧 —— 所以它有没有、开没开，
// 都不影响键盘在动。这是与旧架构最根本的区别。
func ConsoleFS() fs.FS { return embedded }
