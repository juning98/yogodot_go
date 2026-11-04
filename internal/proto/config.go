package proto

import "fmt"

// ConfigLen 是设备配置区的长度。
const ConfigLen = 64

// 配置区字段偏移。与官方 HUB 的布局一致，也是旧版 kbd.py / yogo.html 里散落的那张表。
const (
	OffBLMode      = 2  // 背光模式：0..20 固件内置效果，253=VIA 缓冲区，254=逐键推流
	OffBLBright    = 3  // 背光亮度
	OffBLSpeed     = 4  // 背光速度
	OffBLColorful  = 5  // 背光彩色开关
	OffBLR         = 7  // 背光 R
	OffBLG         = 8  // 背光 G
	OffBLB         = 9  // 背光 B
	OffTFTOnOff    = 29 // LedTFTOnOff：官方设 0 表示启用
	OffDotMode     = 30 // 点阵模式：0..12 固件动画，6=自定义推帧
	OffDotBright   = 31 // 点阵亮度
	OffDotSpeed    = 32 // 点阵速度（存的是 6-ui 的反值）
	OffDotColorful = 33 // 点阵彩色开关
	OffDotColorIdx = 34 // 点阵调色板索引
	OffDotR        = 35 // 点阵 R
	OffDotG        = 36 // 点阵 G
	OffDotB        = 37 // 点阵 B
	OffSleepL      = 46 // 休眠分钟数，24 位小端
	OffSleepM      = 47
	OffSleepH      = 48
)

// 背光模式的几个特殊值。
const (
	BLModeCustomBuffer = 253 // VIA 自定义灯效缓冲区，写一次设备自己显示
	BLModeStream       = 254 // 逐键推流（cmd 46）
)

// DotModeCustom 是点阵的自定义推帧模式。
const DotModeCustom = 6

// Config 是设备配置区的 64 字节快照。
type Config [ConfigLen]byte

// At 返回某个偏移的字节。
func (c Config) At(off int) byte {
	if off < 0 || off >= ConfigLen {
		return 0
	}
	return c[off]
}

// BLMode 返回背光模式。
func (c Config) BLMode() byte { return c[OffBLMode] }

// DotMode 返回点阵模式。
func (c Config) DotMode() byte { return c[OffDotMode] }

// DotBright 返回点阵亮度。
func (c Config) DotBright() byte { return c[OffDotBright] }

// DotSpeedUI 返回点阵速度的 UI 值（0..6）。设备存的是 6-ui 的反值。
func (c Config) DotSpeedUI() int {
	v := int(c[OffDotSpeed])
	if v < 0 {
		v = 0
	}
	if v > 6 {
		v = 6
	}
	return 6 - v
}

// SleepMinutes 返回休眠分钟数。
func (c Config) SleepMinutes() int {
	return int(c[OffSleepL]) | int(c[OffSleepM])<<8 | int(c[OffSleepH])<<16
}

// Patch 是一组"字节偏移 → 值"的修改。
type Patch map[int]byte

// Patch 就地修改配置并返回修改后的副本。越界偏移被忽略。
func (c Config) Patch(p Patch) Config {
	out := c
	for off, v := range p {
		if off >= 0 && off < ConfigLen {
			out[off] = v
		}
	}
	return out
}

// CheckPatch 检查一组修改是否全部落在配置区范围内。
func CheckPatch(p Patch) error {
	for off := range p {
		if off < 0 || off >= ConfigLen {
			return fmt.Errorf("proto: 配置偏移 %d 越界（0..%d）", off, ConfigLen-1)
		}
	}
	return nil
}

// Chunk 描述读/写配置时的分段。三段共 64 字节。
type Chunk struct {
	Offset uint16
	Length byte
}

// ConfigChunks 是读写配置时分包的固定切法。
var ConfigChunks = []Chunk{
	{Offset: 0, Length: 24},
	{Offset: 24, Length: 24},
	{Offset: 48, Length: 16},
}

// DotModePatch 构造"切到某个点阵模式"的配置修改。
//
// mode: 0..12 内置效果，DotModeCustom=6 为自定义推帧。
// speed 是 0..6 的 UI 值，写进设备时取反。
func DotModePatch(mode byte, brightness, speed, colorful, colorIdx *byte, color *[3]byte) Patch {
	p := Patch{
		OffTFTOnOff: 0, // 官方设 0 表示启用
		OffDotMode:  mode,
	}
	if brightness != nil {
		p[OffDotBright] = *brightness
	}
	if speed != nil {
		s := *speed
		if s > 6 {
			s = 6
		}
		p[OffDotSpeed] = 6 - s
	}
	if colorful != nil {
		if *colorful != 0 {
			p[OffDotColorful] = 1
		} else {
			p[OffDotColorful] = 0
		}
	}
	if colorIdx != nil {
		p[OffDotColorIdx] = *colorIdx
	}
	if color != nil {
		p[OffDotR] = color[0]
		p[OffDotG] = color[1]
		p[OffDotB] = color[2]
	}
	return p
}
