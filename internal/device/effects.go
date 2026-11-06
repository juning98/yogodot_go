package device

import (
	"context"

	"github.com/wednesque/yogod/internal/color"
	"github.com/wednesque/yogod/internal/proto"
)

// FirmwareEffect 是键盘固件自带的一个灯效。
//
// 这些效果跑在键盘自己的芯片上：写四个字节过去，主机就可以撒手，
// 一帧都不用再推。代价是配色由固件决定，和我们那套逐键调色板无关。
//
// 名字是从官方驱动的 i18n 里解出来的，不是猜的。
type FirmwareEffect struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	// Colorful 表示这个效果吃自定义颜色（不吃的话配色由固件定）。
	Colorful bool `json:"colorful"`
}

// FirmwareEffects 是固件内置灯效表，顺序即官方驱动里的顺序。
//
// 注意 id 0 是「全灭」不是常亮 —— 这 21 个内置效果里没有真正的常亮，
// 要纯色只能走逐键推流（也就是主题配色那条路）。
var FirmwareEffects = []FirmwareEffect{
	{3, "单色呼吸", true},
	{1, "彩虹流光1", true},
	{7, "彩虹流光2", true},
	{19, "彩虹流光3", true},
	{8, "彩虹流光4", true},
	{4, "彩虹", true},
	{20, "动态彩虹", true},
	{2, "彩色呼吸", false},
	{12, "霓虹", false},
	{5, "按键点亮", true},
	{6, "彩色漩涡1", true},
	{14, "彩色漩涡2", true},
	{9, "光谱飘动1", true},
	{10, "光谱飘动2", true},
	{11, "光谱飘动3", true},
	{16, "光谱扩散1", true},
	{18, "光谱扩散2", true},
	{15, "彩带飘动", true},
	{13, "彩虹雨滴", true},
	{0, "全灭", false},
}

// EffectByID 查一个灯效。
func EffectByID(id int) (FirmwareEffect, bool) {
	for _, e := range FirmwareEffects {
		if e.ID == id {
			return e, true
		}
	}
	return FirmwareEffect{}, false
}

// ApplyFirmwareEffect 把键盘背光交给固件。
//
// light 是这个主题的主色：固件效果不吃自定义颜色时用它当基色，
// 这样切到原厂灯效也不会突然变成一片陌生的配色。
func (c *Controller) ApplyFirmwareEffect(ctx context.Context, eff FirmwareEffect, light color.RGB) error {
	patch := proto.Patch{
		proto.OffBLMode:     byte(eff.ID),
		proto.OffBLBright:   90,
		proto.OffBLColorful: 0,
		proto.OffBLR:        light[0],
		proto.OffBLG:        light[1],
		proto.OffBLB:        light[2],
	}
	if eff.Colorful {
		patch[proto.OffBLColorful] = 1
	}
	_, err := c.PatchConfig(ctx, patch)
	return err
}
