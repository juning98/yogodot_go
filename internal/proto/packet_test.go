package proto

import (
	"bytes"
	"testing"
)

// TestMarshalMatchesPython 对拍旧版 kbd.py 的组包逻辑。
//
// Python 侧（kbd.py:send）：
//
//	pkt = bytearray(33)
//	pkt[0] = 0; pkt[1] = 0xAA; pkt[2] = cmd
//	pkt[3] = off & 255; pkt[4] = (off >> 8) & 255
//	pkt[5] = ln; pkt[6] = seq
//	pkt[9:9+len(pl)] = pl
func TestMarshalMatchesPython(t *testing.T) {
	payload := []byte{1, 2, 3, 4, 5}
	p := New(CmdMatrix, 0x0123, payload)
	p.Seq = 7

	got, err := p.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	want := make([]byte, 33)
	want[0] = 0x00
	want[1] = 0xAA
	want[2] = byte(CmdMatrix)
	want[3] = 0x23
	want[4] = 0x01
	want[5] = 5
	want[6] = 7
	copy(want[9:], payload)

	if !bytes.Equal(got[:], want) {
		t.Fatalf("报文不一致\n got=% X\nwant=% X", got, want)
	}
	if len(got) != WireLen {
		t.Fatalf("线上长度应为 %d，实际 %d", WireLen, len(got))
	}
}

// TestFullPayloadFits 确认 24 字节 payload 正好填满 [9..32]。
func TestFullPayloadFits(t *testing.T) {
	data := make([]byte, PayloadLen)
	for i := range data {
		data[i] = byte(i + 1)
	}
	p := New(CmdMatrix, 0, data)
	p.Seq = 1
	out, err := p.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if out[5] != PayloadLen {
		t.Fatalf("length 字段应为 %d，实际 %d", PayloadLen, out[5])
	}
	if out[32] != byte(PayloadLen) {
		t.Fatalf("最后一字节应为 %d，实际 %d", PayloadLen, out[32])
	}
}

// TestResetDeviceIsRejected 是这次重写最重要的一条护栏。
// 旧版只在注释里写"千万别发"，这里让它在编解码层就发不出去。
func TestResetDeviceIsRejected(t *testing.T) {
	p := New(CmdResetDevice, 0, nil)
	if err := p.Validate(); err == nil {
		t.Fatal("恢复出厂命令必须被拒绝")
	}
	if _, err := p.Marshal(); err == nil {
		t.Fatal("恢复出厂命令必须编不出报文")
	}
}

// TestUnknownCmdRejected 确认表外命令也发不出去。
func TestUnknownCmdRejected(t *testing.T) {
	for _, c := range []Cmd{0, 1, 50, 200, 255} {
		if _, err := New(c, 0, nil).Marshal(); err == nil {
			t.Fatalf("命令 %d 不该被允许", c)
		}
	}
}

// TestTruncateOverlong 确认超长 payload 被截到 24 字节。
func TestTruncateOverlong(t *testing.T) {
	long := bytes.Repeat([]byte{0xAB}, 40)
	p := New(CmdMatrix, 0, long)
	if p.Length != PayloadLen {
		t.Fatalf("length 应被截到 %d，实际 %d", PayloadLen, p.Length)
	}
	out, err := p.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if out[32] != 0xAB {
		t.Fatalf("payload 尾部字节不对：%02X", out[32])
	}
}

// TestUnmarshal 解析一条设备回包。
func TestUnmarshal(t *testing.T) {
	raw := make([]byte, 33)
	raw[0] = 0
	raw[1] = 0xAA
	raw[2] = byte(CmdPowerInfo)
	raw[5] = 2
	raw[6] = 42
	raw[9] = 87 // 电量 87%
	raw[10] = 1 // 供电标志

	p, ok := Unmarshal(raw)
	if !ok {
		t.Fatal("应能解析")
	}
	if p.Cmd != CmdPowerInfo || p.Seq != 42 {
		t.Fatalf("解析结果不对：cmd=%v seq=%d", p.Cmd, p.Seq)
	}
	if p.Data[0] != 87 || p.Data[1] != 1 {
		t.Fatalf("数据区不对：% X", p.Data[:2])
	}
}

// TestUnmarshalRejectsGarbage 确认坏报文不会被当成有效数据。
func TestUnmarshalRejectsGarbage(t *testing.T) {
	if _, ok := Unmarshal(make([]byte, 10)); ok {
		t.Fatal("长度不足应被拒绝")
	}
	b := make([]byte, 33)
	b[1] = 0x55 // 错 magic
	if _, ok := Unmarshal(b); ok {
		t.Fatal("magic 错应被拒绝")
	}
}

// TestSeqRing 确认序号在 1..250 之间循环（与固件期望一致）。
func TestSeqRing(t *testing.T) {
	var r SeqRing
	seen := map[byte]bool{}
	for i := 0; i < 250; i++ {
		v := r.Next()
		if v == 0 || v > 250 {
			t.Fatalf("序号越界：%d", v)
		}
		if seen[v] {
			t.Fatalf("序号重复：%d", v)
		}
		seen[v] = true
	}
	if got := r.Next(); got != 1 {
		t.Fatalf("应回绕到 1，实际 %d", got)
	}
}
