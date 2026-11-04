package device

import (
	"github.com/wednesque/yogod/internal/frame"
	"github.com/wednesque/yogod/internal/proto"
)

// 一帧要切成几包。
//
//	点阵 108 字节 / 24 = 4.5 → 5 包（最后一包只有 12 字节有效，其余补 0）
//	逐键 168 字节 / 24 = 7   → 7 包（正好整除）
const (
	MatrixPacketCount = (frame.MatrixLen + proto.PayloadLen - 1) / proto.PayloadLen
	KeysPacketCount   = (frame.KeysLen + proto.PayloadLen - 1) / proto.PayloadLen
)

// MatrixPackets 把一帧点阵切成待发送的报文（偏移已填好，seq 留空）。
//
// 抽成纯函数有两个原因：一是不需要真设备就能测分包是否正确，
// 二是旧版把这件事散在循环里，最后一包长度算错会直接 panic 或让屏幕缺一块。
func MatrixPackets(m frame.Matrix) []proto.Packet {
	out := make([]proto.Packet, 0, MatrixPacketCount)
	for i := 0; i < MatrixPacketCount; i++ {
		off := i * proto.PayloadLen
		var chunk [proto.PayloadLen]byte
		if off < frame.MatrixLen {
			copy(chunk[:], m[off:]) // 最后一包自然截短，剩余保持 0
		}
		p := proto.New(proto.CmdMatrix, uint16(off), chunk[:])
		p.Length = proto.PayloadLen // 固件按定长收，不缩短
		out = append(out, p)
	}
	return out
}

// KeysPackets 把一帧键盘灯切成待发送的报文。
func KeysPackets(k frame.Keys) []proto.Packet {
	out := make([]proto.Packet, 0, KeysPacketCount)
	for i := 0; i < KeysPacketCount; i++ {
		off := i * proto.PayloadLen
		var chunk [proto.PayloadLen]byte
		if off < frame.KeysLen {
			copy(chunk[:], k[off:])
		}
		p := proto.New(proto.CmdSyncLED, uint16(off), chunk[:])
		p.Length = proto.PayloadLen
		out = append(out, p)
	}
	return out
}
