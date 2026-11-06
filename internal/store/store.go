// Package store 是所有可写状态的唯一出口。
//
// 旧版的设置散落在五个地方：Chrome profile 里的 23 个 localStorage 键、
// settings.json、capsule.json、config.bak、.token。
// 结果是"改了没生效"和"换个浏览器就丢设置"这两类 bug 反复出现。
//
// 这里只有一份 settings.json，带版本号、原子写、可迁移。
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Version 是当前配置格式版本。改结构时 +1 并在 migrate 里处理。
const Version = 1

// 文件名。
const (
	SettingsFile = "settings.json"
	// KeyboardBackupFile 是键盘 64 字节原始配置的备份。
	// write_config 是读-改-写，写坏了没处找原始值 —— 第一次读到就落一份。
	KeyboardBackupFile = "config.bak"
)

// Brightness 是两路输出的软件亮度（0..100）。
type Brightness struct {
	Dot  float64 `json:"dot"`
	Keys float64 `json:"keys"`
}

// Calibration 是灯珠调色参数。默认值来自原项目的实测标定。
type Calibration struct {
	// Gain 是通道增益（白平衡）。这批灯珠绿、蓝的发光效率远高于红。
	Gain [3]float64 `json:"gain"`
	// Corr 是校正强度 0..1：暖色亮度 ↔ 色相准确度，二选一。
	Corr float64 `json:"corr"`
	// Sat 是饱和度倍数。低饱和色在灯珠上会发白。
	Sat float64 `json:"sat"`
	// EQL 是感知光量水位。
	EQL float64 `json:"eql"`
	// DotCalib 决定点阵是否也套用同一套校正。
	// 默认 false：点阵屏实测混色本来就准，套上去反而搞坏。
	DotCalib bool `json:"dot_calib"`
}

// Idle 是省电策略。
type Idle struct {
	// BlankSeconds 是多久没碰键鼠就熄屏（0 = 用默认 180）。
	BlankSeconds int `json:"blank_seconds"`
	// KeysOffPercent / AllOffPercent 是两级电量阈值。
	KeysOffPercent int `json:"keys_off_percent"`
	AllOffPercent  int `json:"all_off_percent"`
}

// Settings 是整个程序的配置。
type Outputs struct {
	Dot  bool `json:"dot"`
	Keys bool `json:"keys"`
}

type Config struct {
	Outputs Outputs `json:"outputs"`
	Version int     `json:"version"`

	// Theme 是当前主题 id。
	Theme string `json:"theme"`
	// Mode 是当前场景："theme" / "focus" / "pomodoro" / "companion"。
	Mode string `json:"mode"`
	// Dynamic 是"会动的主题"开关。
	Dynamic bool `json:"dynamic"`
	// KBEffect 是原厂内置键盘灯效的 id；nil 表示用主题的逐键配色。
	// 用指针是为了把"没设置"和"设成 0（全灭）"分开 —— 老配置里没有这个字段，
	// 读出来必须是 nil，不能默认成 0 把人的灯灭了。
	KBEffect *int `json:"kb_effect,omitempty"`

	Bright     Brightness  `json:"brightness"`
	Calib      Calibration `json:"calibration"`
	IdlePolicy Idle        `json:"idle"`

	// TypingGuard 是打字保护：敲键盘时暂停推帧，停手 250ms 后补发。
	// 关掉之后动画不被打字打断。
	// 默认关：限速本身就是带宽管家，信号好时保护纯属打断动画；
	// 2.4G 信号差、感觉击键被灯效拖卡时再打开。
	TypingGuard bool `json:"typing_guard"`

	// RateTier 是推帧档位：0 默认（点阵 6.25 / 键灯 10 fps）、
	// 1 流畅（10 / 20 fps）、2 极速（15 / 25 fps）。
	// 档位语义在 device.RateTier；这里只存编号。
	RateTier int `json:"rate_tier"`

	// Pattern 是图案库的单选图案 id，非空时点阵屏显示它而不是主题默认点阵；
	// 键盘背光仍走主题。空串 = 未选。
	Pattern string `json:"pattern"`

	// IdleAgent 是伴侣场景空闲时显示的工具形状 id；
	// 空串 = 跟随最近会话（没有会话或不认识时显示兜底宝石）。
	IdleAgent string `json:"idle_agent"`
}

// Settings 将内存快照锁与串行落盘锁分开。
type Settings struct {
	Config
	saveMu sync.Mutex
	// path 是这份配置写在哪，不参与序列化。
	path string
	mu   sync.Mutex
}

// Defaults 返回默认配置。
func Defaults() Settings {
	return Settings{Config: Config{
		Version: Version,
		Theme:   "pikachu",
		Mode:    "theme",
		Dynamic: true,
		Bright:  Brightness{Dot: 50, Keys: 70},
		Calib: Calibration{
			Gain: [3]float64{1.0, 0.50, 0.42},
			Corr: 0.70,
			Sat:  1.80,
			EQL:  255,
		},
		IdlePolicy: Idle{
			BlankSeconds:   180,
			KeysOffPercent: 20,
			AllOffPercent:  8,
		},
		// 打字保护默认关：键灯 2fps / 点阵 6.25fps 的限速本来就是带宽管家,
		// 保护省下的带宽是零头,代价却是打字时动画定格。信号差时链路
		// 本身卡,保护也救不了。需要时在控制台键盘页打开。
		TypingGuard: false,
		Outputs:     Outputs{Dot: true, Keys: true},
	}}
}

// Open 读取（或初始化）dir 下的配置。
//
// 文件不存在不算错误：返回默认值并把 path 记下来，第一次 Save 时落盘。
func Open(dir string) (*Settings, error) {
	s := Defaults()
	s.path = filepath.Join(dir, SettingsFile)

	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return &s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: 读配置失败: %w", err)
	}

	// 先读进默认值的副本，缺字段就保留默认 —— 这样加字段不会让老文件失效
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("store: 配置格式损坏（%s）：%w", s.path, err)
	}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// migrate 处理版本升级。目前只有 v1，留着框架。
func (s *Settings) migrate() error {
	if s.Version == 0 {
		s.Version = Version
	}
	if s.Version > Version {
		return fmt.Errorf("store: 配置版本 %d 比程序（%d）新，请升级程序", s.Version, Version)
	}
	// 未来：if s.Version < 2 { ... ; s.Version = 2 }
	return nil
}

// Path 返回配置文件路径。
func (s *Settings) Path() string { return s.path }

// Update 在锁内改配置。所有写方（HTTP handler、托盘、场景重建）都必须走这里：
// Settings 被 serve 的 goroutine 和 api 的 handler goroutine 共享，
// 直接改字段是数据竞争 —— Save 自己的锁管不了不写锁的调用方。
// 回调里不要再调 Save（同一把锁，会死锁）；改完在外层 Save。
func (s *Settings) Update(f func(*Settings)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

// View 在锁内读配置。运行期读方（state 组装、场景重建）走这里。
func (s *Settings) Snapshot() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.Config
	if c.KBEffect != nil {
		id := *c.KBEffect
		c.KBEffect = &id
	}
	return c
}
func (s *Settings) View(f func(*Settings)) { c := Settings{Config: s.Snapshot()}; f(&c) }

// Save 原子地写回配置。
//
// 先写临时文件再 Rename：中途崩了也不会留下半个 JSON，
// 旧版直接 json.dump 打开的文件，崩在写一半就是配置文件废掉。
func (s *Settings) Save() error {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	snapshot := s.Snapshot()

	if s.path == "" {
		return errors.New("store: 没有配置路径")
	}
	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')

	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".settings-*.tmp")
	if err != nil {
		return fmt.Errorf("store: 建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // Rename 成功后这里是空操作

	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}

// ── 键盘配置备份 ──────────────────────────────────────────────────

// SaveKeyboardBackup 在还没备份过时落一份键盘原始配置。
func SaveKeyboardBackup(dir string, cfg []byte) error {
	p := filepath.Join(dir, KeyboardBackupFile)
	if _, err := os.Stat(p); err == nil {
		return nil // 已经有备份，绝不覆盖
	}
	return os.WriteFile(p, cfg, 0o644)
}

// LoadKeyboardBackup 读回键盘原始配置。
func LoadKeyboardBackup(dir string) ([]byte, error) {
	raw, err := os.ReadFile(filepath.Join(dir, KeyboardBackupFile))
	if err != nil {
		return nil, err
	}
	if len(raw) != 64 {
		return nil, fmt.Errorf("store: 备份不是 64 字节（实际 %d）", len(raw))
	}
	return raw, nil
}

// PortableMarker 是便携模式的标记文件名：在 exe 旁边放一个名为 "portable"
// 的空文件，配置/日志/图标就全程跟着 exe 走（首跑之前就生效）。
const PortableMarker = "portable"

// DefaultDir 返回配置与日志的存放目录。
//
// 两个位置：
//   - 便携模式：exe 旁边已有 settings.json（老安装原地升级），
//     或者放了 portable 标记文件（主动选择，先建标记再首跑）→ 数据跟 exe 走；
//   - 否则 %LOCALAPPDATA%\yogod —— zip 解到下载目录双击就跑的话,
//     不该把 settings.json / yogod.log 撒进下载目录里。
func DefaultDir() string {
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		_, hasSettings := os.Stat(filepath.Join(dir, SettingsFile))
		_, hasMarker := os.Stat(filepath.Join(dir, PortableMarker))
		if (hasSettings == nil || hasMarker == nil) && writable(dir) {
			return dir
		}
	}
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = home
	}
	dir := filepath.Join(base, "yogod")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".wtest-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}
