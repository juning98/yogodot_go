//go:build windows

package sys

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// StartupShortcut 返回开机自启快捷方式的路径。
func StartupShortcut() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return ""
	}
	return filepath.Join(appData, "Microsoft", "Windows", "Start Menu",
		"Programs", "Startup", "yogod.lnk")
}

// AutostartEnabled 报告是否已开启开机自启。
func AutostartEnabled() bool {
	p := StartupShortcut()
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

// SetAutostart 开/关开机自启。
//
// 用 PowerShell 的 WScript.Shell 建快捷方式 —— 这是 Windows 上
// 不需要 COM 绑定就能建 .lnk 的办法。失败不影响主功能，只记日志。
func SetAutostart(on bool) error {
	p := StartupShortcut()
	if p == "" {
		return fmt.Errorf("sys: 找不到启动目录")
	}
	if !on {
		err := os.Remove(p)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir := filepath.Dir(exe)
	ps := fmt.Sprintf(
		"$s=(New-Object -ComObject WScript.Shell).CreateShortcut('%s');"+
			"$s.TargetPath='%s';$s.WorkingDirectory='%s';"+
			"$s.IconLocation='%s';$s.Save()",
		escapePS(p), escapePS(exe), escapePS(dir), escapePS(exe))

	return runHidden("powershell", "-NoProfile", "-Command", ps)
}

// escapePS 把单引号转义成 PowerShell 的单引号写法。
func escapePS(s string) string { return strings.ReplaceAll(s, "'", "''") }
