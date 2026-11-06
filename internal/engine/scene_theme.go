package engine

import (
	"github.com/wednesque/yogod/internal/color"
	"github.com/wednesque/yogod/internal/device"
	"github.com/wednesque/yogod/internal/frame"
	"github.com/wednesque/yogod/internal/theme"
	"time"
)

// ThemeScene 只描述画面与模式；全局输出管线负责亮度、校准和设备写入。
type ThemeScene struct {
	Theme      theme.Theme
	Dynamic    bool
	KBFirmware *device.FirmwareEffect
	KBLight    color.RGB
	Pattern    *theme.Pattern
}

func NewThemeScene(t theme.Theme, dynamic bool) *ThemeScene {
	return &ThemeScene{Theme: t, Dynamic: dynamic, KBLight: t.Light}
}
func (s *ThemeScene) Name() string { return "theme:" + s.Theme.ID }
func (s *ThemeScene) Animation() Channels {
	return Channels{s.Dynamic && s.Pattern == nil && s.Theme.DotAnim != nil, s.Dynamic && s.KBFirmware == nil && s.Theme.KBAnim != nil}
}
func (s *ThemeScene) Mode() device.Mode {
	m := device.Mode{Matrix: true, Keys: true, Effect: -1}
	if s.KBFirmware != nil {
		m.Effect = s.KBFirmware.ID
		m.Light = s.KBLight
		m.Keys = m.Effect != 0
	}
	return m
}
func (s *ThemeScene) Frame(t time.Duration, ch Channels) Picture {
	var r Picture
	if ch.Matrix {
		var g frame.Grid
		switch {
		case s.Pattern != nil:
			g = theme.DotFromArt(s.Pattern.Art)
		case s.Dynamic:
			g = s.Theme.DotAt(t.Seconds())
		default:
			g = s.Theme.DotFrame()
		}
		r.Matrix = &g
	}
	if ch.Keys && s.KBFirmware == nil {
		var l frame.LEDs
		if s.Dynamic {
			l = s.Theme.KBAt(t.Seconds())
		} else {
			l = s.Theme.KBFrames()
		}
		r.Keys = &l
	}
	return r
}

type FocusScene struct{}

func (*FocusScene) Name() string                          { return "focus" }
func (*FocusScene) Animation() Channels                   { return Channels{} }
func (*FocusScene) Mode() device.Mode                     { return device.Mode{Effect: -1} }
func (*FocusScene) Frame(time.Duration, Channels) Picture { return Picture{} }
