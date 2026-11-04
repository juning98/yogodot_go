package device

import (
	"testing"

	"github.com/wednesque/yogod/internal/frame"
	"github.com/wednesque/yogod/internal/proto"
)

// TestMatrixPacketization 是这条链路上最容易出错的一处。
//
// 108 字节 ÷ 24 = 4.5，所以是 5 包，最后一包只有 12 字节有效。
// 旧版的 Python 用 `flat[i*24:i*24+24].ljust(24, NUL)` 顺手处理了越界，
// 照抄成 Go 的切片就会 panic（`slice bounds out of range [:120] with length 108`）——
// 这个 bug 在本机冒烟运行时真的炸过一次。
func TestMatrixPacketization(t *testing.T) {
	var m frame.Matrix
	for i := range m {
		m[i] = byte(i)
	}

	pkts := MatrixPackets(m)
	if len(pkts) != 5 {
		t.Fatalf("应切成 5 包，实际 %d", len(pkts))
	}

	var rebuilt [frame.MatrixLen]byte
	for i, p := range pkts {
		if p.Cmd != proto.CmdMatrix {
			t.Errorf("第 %d 包命令号错：%v", i, p.Cmd)
		}
		wantOff := uint16(i * proto.PayloadLen)
		if p.Offset != wantOff {
			t.Errorf("第 %d 包偏移错：%d，期望 %d", i, p.Offset, wantOff)
		}
		// 固件按定长收，length 必须一直是 24
		if p.Length != proto.PayloadLen {
			t.Errorf("第 %d 包 length=%d，期望 %d", i, p.Length, proto.PayloadLen)
		}
		// 拼回去应该和原帧一致
		for j := 0; j < proto.PayloadLen; j++ {
			idx := int(wantOff) + j
			if idx < frame.MatrixLen {
				if p.Data[j] != m[idx] {
					t.Errorf("第 %d 包第 %d 字节：%d，期望 %d", i, j, p.Data[j], m[idx])
				}
				rebuilt[idx] = p.Data[j]
			}
		}
	}
	if rebuilt != m {
		t.Fatal("分包再拼回去与原帧不一致")
	}

	// 最后一包的尾巴必须补 0，不能带垃圾
	last := pkts[4]
	if last.Data[12] != 0 || last.Data[23] != 0 {
		t.Errorf("最后一包的补位不是 0：% x", last.Data)
	}
}

// TestKeysPacketization 验证逐键帧分包（168 = 7×24，正好整除）。
func TestKeysPacketization(t *testing.T) {
	var k frame.Keys
	for i := range k {
		k[i] = byte(i)
	}

	pkts := KeysPackets(k)
	if len(pkts) != 7 {
		t.Fatalf("应切成 7 包，实际 %d", len(pkts))
	}
	for i, p := range pkts {
		if p.Cmd != proto.CmdSyncLED {
			t.Errorf("第 %d 包命令号错：%v", i, p.Cmd)
		}
		if p.Offset != uint16(i*proto.PayloadLen) {
			t.Errorf("第 %d 包偏移错：%d", i, p.Offset)
		}
		if p.Length != proto.PayloadLen {
			t.Errorf("第 %d 包 length=%d", i, p.Length)
		}
	}
}

// TestPacketsAreSendable 确认每一包都能通过协议层的白名单校验。
func TestPacketsAreSendable(t *testing.T) {
	for _, p := range MatrixPackets(frame.Matrix{}) {
		if _, err := p.Marshal(); err != nil {
			t.Fatalf("点阵包不可发送：%v", err)
		}
	}
	for _, p := range KeysPackets(frame.Keys{}) {
		if _, err := p.Marshal(); err != nil {
			t.Fatalf("逐键包不可发送：%v", err)
		}
	}
}
