package main

import (
	"github.com/wednesque/yogod/internal/color"
)

// u8 返回一个字节的指针（协议里不少字段是可选的）。
func u8(v int) *byte {
	b := byte(v)
	return &b
}

// deviceCal 返回默认校准参数。
// 以后接 settings 时从这里换成用户保存的值。
func deviceCal() color.Calibration { return color.DefaultCalibration() }
