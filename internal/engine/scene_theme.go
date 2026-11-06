package engine

import (
	"context"
	"time"

	"github.com/wednesque/yogod/internal/color"
	"github.com/wednesque/yogod/internal/device"
	"github.com/wednesque/yogod/internal/frame"
	"github.com/wednesque/yogod/internal/proto"
	"github.com/wednesque/yogod/internal/theme"
)

// ThemeScene 把一套主题渲染到点阵屏 + 键盘背光。
//
// 静态主题：推一帧就完事，之后零流量（device 层会去重，引擎也不会重复写）。
// 动态主题：点阵和背光一起动，按引擎的 30Hz 出帧、由 device 限速到 6.25/2 fps。
type ThemeScene struct {
	Theme theme.Theme
	// Dynamic 为 false 时强制用静态图（对应旧版的"静态/动态"开关）。
	Dynamic bool

	Cal       color.Calibration
	DotBright float64
	KBBright  float64
	// ApplyDotCalib 决定点阵是否也套用键盘那套校正。
	// 默认 false：点阵屏实测混色本来就准，套上去反而搞坏。
	ApplyDotCalib bool
	// KBFirmware 非 nil 时，键盘背光交给这个固件灯效自己跑：
	// 不再推逐键配色（一帧都不推），只继续推点阵。
	KBFirmware *device.FirmwareEffect
	// KBLight 是固件效果不吃自定义颜色时的基色（取主题主色，
	// 避免切到原厂灯效后配色突然变成一片陌生颜色）。
	KBLight color.RGB
	// Pattern 非 nil 时点阵屏改显示这张图案（键盘背光仍走主题）。
	// 用户从图案库单选推送；置 nil 恢复主题的默认点阵。
	Pattern *theme.Pattern
}

// NewThemeScene 用一套主题建场景。
func NewThemeScene(t theme.Theme, dynamic bool, cal color.Calibration) *ThemeScene {
	return &ThemeScene{
		Theme:     t,
		Dynamic:   dynamic,
		Cal:       cal,
		DotBright: 50, // 默认压到一半：LED 电流跟亮度基本线性
		KBBright:  70,
	}
}

// Name 实现 Scene。
func (s *ThemeScene) Name() string { return "theme:" + s.Theme.ID }

// Frame 实现 Scene。纯函数，不碰设备。
func (s *ThemeScene) Frame(t time.Duration) Render {
	sec := t.Seconds()

	var g frame.Grid
	var l frame.LEDs
	switch {
	case s.Pattern != nil:
		// 图案接管点阵：图案本身是静态图，背光仍按动态/静态走主题。
		g = theme.DotFromArt(s.Pattern.Art)
		if s.Dynamic {
			l = s.Theme.KBAt(sec)
		} else {
			l = s.Theme.KBFrames()
		}
	case s.Dynamic:
		g = s.Theme.DotAt(sec)
		l = s.Theme.KBAt(sec)
	default:
		g = s.Theme.DotFrame()
		l = s.Theme.KBFrames()
	}

	var m frame.Matrix
	if s.Dynamic && s.Pattern == nil {
		m = g.EncodeAnimated(s.Cal, s.DotBright, s.ApplyDotCalib)
	} else {
		m = g.Encode(s.Cal, s.DotBright, s.ApplyDotCalib)
	}
	// 预览帧直通原色：校准（白平衡/EQL 限流/饱和度补偿）和亮度都是给
	// 灯珠的电气补偿，屏幕不需要 —— 页面显示主题本来的颜色。
	pm := g.Encode(s.Cal, 100, false)
	r := Render{Matrix: &m, PrevMatrix: &pm}
	if s.KBFirmware == nil {
		k := l.Encode(s.Cal, s.KBBright)
		pk := frame.RawKeys(l, 100)
		r.Keys = &k
		r.PrevKeys = &pk
	}
	return r
}

// Enter 实现 Scene：把设备切到"接受推帧"的模式。
//
// 这是修 P0-1 路径 B 的关键一步：设备模式**每次进场景都重新写**，
// 而不是像旧版那样"缓存住就算数"。成本是三次配置写入，
// 收益是键盘休眠/被别人抢过之后能自己恢复。
func (s *ThemeScene) Enter(ctx context.Context, ctrl *device.Controller) error {
	// 与官方自定义点阵会话保持一致：先结束旧同步，再写模式并开始推帧。
	if err := ctrl.EndDotSync(ctx); err != nil {
		return err
	}
	patch := proto.Patch{
		proto.OffBLBright:   90,
		proto.OffBLColorful: 0,
	}
	if s.KBFirmware != nil {
		// 背光交给固件：写效果号，而不是推流模式 254。
		// 写 254 会让固件等我们的帧，固件效果就永远不跑。
		patch[proto.OffBLMode] = byte(s.KBFirmware.ID)
		patch[proto.OffBLR] = s.KBLight[0]
		patch[proto.OffBLG] = s.KBLight[1]
		patch[proto.OffBLB] = s.KBLight[2]
		if s.KBFirmware.Colorful {
			patch[proto.OffBLColorful] = 1
		}
	} else {
		patch[proto.OffBLMode] = proto.BLModeStream
	}
	for k, v := range proto.DotModePatch(
		proto.DotModeCustom, u8(90), u8(3), nil, nil, nil) {
		patch[k] = v
	}
	_, err := ctrl.PatchConfig(ctx, patch)
	return err
}

// Exit 实现 Scene。离开主题场景时把背光交回固件，免得旧配色被推回去。
func (s *ThemeScene) Exit(ctx context.Context, ctrl *device.Controller) {
	ctrl.ForgetKeys()
}

// FocusScene 是全灭场景：键盘和屏幕都关掉，什么都不动。
type FocusScene struct{}

// Name 实现 Scene。
func (s *FocusScene) Name() string { return "focus" }

// Frame 实现 Scene。
func (s *FocusScene) Frame(time.Duration) Render {
	m := frame.Matrix{}
	k := frame.Keys{}
	return Render{Matrix: &m, Keys: &k}
}

// Enter 实现 Scene：写背光模式 0，让固件把 LED 驱动真的关掉。
//
// 只在推流模式下推一帧全黑是不够的 —— 灯是黑的，驱动和推流态都还醒着，电照样掉。
func (s *FocusScene) Enter(ctx context.Context, ctrl *device.Controller) error {
	_, err := ctrl.PatchConfig(ctx, proto.Patch{
		proto.OffBLMode:   0,
		proto.OffDotMode:  proto.DotModeCustom,
		proto.OffTFTOnOff: 0,
	})
	if err != nil {
		return err
	}
	// 进自定义点阵前官方会先发 end_sync_dot
	return nil
}

// Exit 实现 Scene。
func (s *FocusScene) Exit(context.Context, *device.Controller) {}

func u8(v int) *byte {
	b := byte(v)
	return &b
}
