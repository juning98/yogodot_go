package store

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDefaultsRoundTrip 确认默认配置能序列化再读回来。
func TestDefaultsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := Defaults()
	s.path = filepath.Join(dir, SettingsFile)
	s.Theme = "claude"
	s.Bright.Dot = 42

	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got.Theme != "claude" {
		t.Errorf("Theme = %q，期望 claude", got.Theme)
	}
	if got.Bright.Dot != 42 {
		t.Errorf("Dot 亮度 = %v，期望 42", got.Bright.Dot)
	}
	if got.Calib.Gain != [3]float64{1, 0.50, 0.42} {
		t.Errorf("Gain 丢了：%v", got.Calib.Gain)
	}
}

// TestMissingFileUsesDefaults 确认第一次运行不报错。
func TestMissingFileUsesDefaults(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if s.Theme != "pikachu" {
		t.Errorf("默认主题应为 pikachu，实际 %q", s.Theme)
	}
	if s.Version != Version {
		t.Errorf("版本应为 %d，实际 %d", Version, s.Version)
	}
}

// TestPartialFileKeepsDefaults 确认老配置文件缺字段时不会把新字段清零。
//
// 这就是"用默认值打底再 Unmarshal"的意义：加字段不需要写迁移代码。
func TestPartialFileKeepsDefaults(t *testing.T) {
	dir := t.TempDir()
	// 只有 theme，没有 brightness / calibration
	raw := []byte(`{"version":1,"theme":"matrix"}`)
	if err := os.WriteFile(filepath.Join(dir, SettingsFile), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if s.Theme != "matrix" {
		t.Errorf("Theme = %q，期望 matrix", s.Theme)
	}
	if s.Bright.Keys != 70 {
		t.Errorf("缺的字段应保留默认值 70，实际 %v", s.Bright.Keys)
	}
	if s.Calib.Sat != 1.80 {
		t.Errorf("Calib.Sat 应保留默认 1.80，实际 %v", s.Calib.Sat)
	}
}

// 打字保护默认关（2026-11 起）：老配置里没有 typing_guard 字段,
// 读出来必须是关(false),跟新默认对齐;开过的用户文件里写着 true,不受影响。
func TestTypingGuardDefaultsOff(t *testing.T) {
	dir := t.TempDir()
	raw := []byte(`{"version":1,"theme":"matrix"}`)
	if err := os.WriteFile(filepath.Join(dir, SettingsFile), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if s.TypingGuard {
		t.Error("老配置缺 typing_guard 时应默认关,实际是开")
	}
}

// TestCorruptFileIsAnError 确认损坏的配置不会被静默当成默认值。
//
// 静默重置会让用户"设置莫名奇妙全没了"，还不如明确报错。
func TestCorruptFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, SettingsFile), []byte("{不是 json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("损坏的配置应返回错误")
	}
}

// TestNewerVersionIsRejected 确认不会用旧程序去糟蹋新格式的配置。
func TestNewerVersionIsRejected(t *testing.T) {
	dir := t.TempDir()
	raw := []byte(`{"version":99,"theme":"x"}`)
	if err := os.WriteFile(filepath.Join(dir, SettingsFile), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("更高版本的配置应被拒绝")
	}
}

// TestSaveIsAtomic 确认写完不会留下临时文件。
func TestSaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	s := Defaults()
	s.path = filepath.Join(dir, SettingsFile)
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != SettingsFile {
			t.Errorf("留下了多余的文件：%s", e.Name())
		}
	}
}

// TestKeyboardBackupIsNeverOverwritten 确认原始配置备份只写一次。
//
// 这是安全资产：write_config 是读-改-写，写坏了只能靠它恢复。
func TestKeyboardBackupIsNeverOverwritten(t *testing.T) {
	dir := t.TempDir()
	first := make([]byte, 64)
	first[2] = 7 // blMode

	if err := SaveKeyboardBackup(dir, first); err != nil {
		t.Fatalf("首次备份失败：%v", err)
	}

	second := make([]byte, 64)
	second[2] = 99
	if err := SaveKeyboardBackup(dir, second); err != nil {
		t.Fatalf("第二次备份失败：%v", err)
	}

	got, err := LoadKeyboardBackup(dir)
	if err != nil {
		t.Fatalf("读备份失败：%v", err)
	}
	if got[2] != 7 {
		t.Fatalf("备份被覆盖了：blMode = %d，应为 7", got[2])
	}
}

// TestKeyboardBackupRejectsWrongSize 确认非 64 字节的备份不会被当成有效。
func TestKeyboardBackupRejectsWrongSize(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, KeyboardBackupFile), make([]byte, 32), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKeyboardBackup(dir); err == nil {
		t.Fatal("32 字节的备份应被拒绝")
	}
}

// 推帧档位默认 0（默认档）：老配置里没有 rate_tier 字段，
// 读出来必须是 0，跟新默认对齐。
func TestRateTierDefaultsZero(t *testing.T) {
	dir := t.TempDir()
	raw := []byte(`{"version":1,"theme":"matrix"}`)
	if err := os.WriteFile(filepath.Join(dir, SettingsFile), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if s.RateTier != 0 {
		t.Errorf("老配置缺 rate_tier 时应为 0，实际 %d", s.RateTier)
	}
}

// 图案单选默认空串：老配置里没有 pattern 字段，
// 读出来必须是空（= 点阵走主题默认）。
func TestPatternDefaultsEmpty(t *testing.T) {
	dir := t.TempDir()
	raw := []byte(`{"version":1,"theme":"matrix"}`)
	if err := os.WriteFile(filepath.Join(dir, SettingsFile), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if s.Pattern != "" {
		t.Errorf("老配置缺 pattern 时应为空串，实际 %q", s.Pattern)
	}
}
