// Package proto 实现 ATK YOGO 75 PRO 的 HID 报文编解码。
//
// 这一层是纯函数，不碰设备也不碰网络，所有平台都能编译、都该被单元测试覆盖。
// 协议知识（命令号、字段偏移、帧长度）**只在这里出现一次**——
// 旧版把它抄在 kbd.py / server.py / yogo.html 三个地方，改一处漏两处。
//
//	线上报文 33 字节：
//	  [0]    report id (0x00)
//	  [1]    magic (0xAA)
//	  [2]    cmd
//	  [3..4] offset, 小端
//	  [5]    length
//	  [6]    seq
//	  [7..8] 保留 0
//	  [9..32] payload（24 字节）
//
//	回包：seq 在 [6]，数据区从 [9] 起。
package proto

import (
	"errors"
	"fmt"
)

// 报文几何。
const (
	// ReportID 是 HID 报告 ID，固定 0。
	ReportID byte = 0x00
	// Magic 是厂商通道的固定前缀。
	Magic byte = 0xAA
	// PayloadLen 是单包可携带的数据长度。
	PayloadLen = 24
	// PacketLen 是 payload 之外的头加尾，即 9 + 24。
	// 保留旧名字，等价于 HeaderLen，供调用方判断"一包能装多少"。
	HeaderLen = 9
	// WireLen 是线上实际写出的长度：9 字节头 + 24 字节 payload。
	WireLen = HeaderLen + PayloadLen
)

// Cmd 是厂商通道的命令号。
type Cmd byte

// 命令表。取自官方 Web HUB 3.2.28 的前端，实测校准过。
//
// 注意 CmdResetDevice 就夹在常用号段中间：**任何情况下都不发**。
// 见 allowed 表 —— 它在 Validate 阶段就会被拒绝，不依赖调用方自觉。
const (
	CmdHandshake   Cmd = 16 // 握手
	CmdReadConfig  Cmd = 20 // 读 64B 配置
	CmdWriteConfig Cmd = 21 // 写 64B 配置
	CmdSyncLED     Cmd = 46 // 逐键推流（168B RGB565）
	CmdEndSyncLED  Cmd = 47 // 结束逐键推流
	CmdPowerInfo   Cmd = 48 // 电量与供电状态
	CmdResetDevice Cmd = 49 // 恢复出厂 —— 禁止发送
	CmdMatrix      Cmd = 59 // 点阵帧（108B RGB888）
	CmdEndSyncDot  Cmd = 61 // 结束点阵同步会话
)

var cmdNames = map[Cmd]string{
	CmdHandshake:   "handshake",
	CmdReadConfig:  "read_config",
	CmdWriteConfig: "write_config",
	CmdSyncLED:     "open_sync_led",
	CmdEndSyncLED:  "end_sync_led",
	CmdPowerInfo:   "power_info",
	CmdResetDevice: "reset_device",
	CmdMatrix:      "set_dotscreen_matrix",
	CmdEndSyncDot:  "end_sync_dot",
}

// allowed 是**可以**发出去的命令白名单。CmdResetDevice 刻意不在其中。
var allowed = map[Cmd]bool{
	CmdHandshake:   true,
	CmdReadConfig:  true,
	CmdWriteConfig: true,
	CmdSyncLED:     true,
	CmdEndSyncLED:  true,
	CmdPowerInfo:   true,
	CmdMatrix:      true,
	CmdEndSyncDot:  true,
}

// String 返回人类可读的命令名。
func (c Cmd) String() string {
	if n, ok := cmdNames[c]; ok {
		return n
	}
	return fmt.Sprintf("cmd(%d)", byte(c))
}

// ErrForbiddenCmd 表示命令被白名单拒绝（例如恢复出厂）。
var ErrForbiddenCmd = errors.New("proto: command not in allowlist")

// Packet 是一条待发送或刚解析出的厂商通道报文。
type Packet struct {
	Cmd    Cmd
	Offset uint16
	Length byte
	Seq    byte
	Data   [PayloadLen]byte
}

// New 构造一条报文，data 超过 24 字节会被截断（协议本身就是分包的）。
func New(cmd Cmd, offset uint16, data []byte) Packet {
	var p Packet
	p.Cmd = cmd
	p.Offset = offset
	p.Data = payloadN(data)
	if len(data) > PayloadLen {
		p.Length = PayloadLen
	} else {
		p.Length = byte(len(data))
	}
	return p
}

// NewLen 构造一条报文，但显式指定 length 字段（读配置时 len 与 data 长度一致，
// 但结束同步类命令需要 length=0 而 payload 为空）。
func NewLen(cmd Cmd, offset uint16, length byte, data []byte) Packet {
	p := New(cmd, offset, data)
	p.Length = length
	return p
}

func payloadN(b []byte) [PayloadLen]byte {
	var out [PayloadLen]byte
	copy(out[:], b)
	return out
}

// Validate 检查这条报文是否允许出现在线上。
func (p Packet) Validate() error {
	if !allowed[p.Cmd] {
		if p.Cmd == CmdResetDevice {
			return fmt.Errorf("%w: %s (%d) 会恢复出厂设置，永远不要发送",
				ErrForbiddenCmd, p.Cmd, byte(p.Cmd))
		}
		return fmt.Errorf("%w: %s (%d) 不在已知命令表内", ErrForbiddenCmd, p.Cmd, byte(p.Cmd))
	}
	return nil
}

// Marshal 把报文编码成线上 33 字节。会先过 Validate。
func (p Packet) Marshal() ([WireLen]byte, error) {
	var out [WireLen]byte
	if err := p.Validate(); err != nil {
		return out, err
	}
	out[0] = ReportID
	out[1] = Magic
	out[2] = byte(p.Cmd)
	out[3] = byte(p.Offset & 0xFF)
	out[4] = byte(p.Offset >> 8)
	out[5] = p.Length
	out[6] = p.Seq
	// [7..8] 保留 0
	copy(out[9:], p.Data[:])
	return out, nil
}

// Unmarshal 解析一条回包。第二个返回值表示是否是一条合法报文（magic 正确）。
// 不校验 cmd 白名单 —— 设备可能回出表外的号，解析层不该因此失败。
func Unmarshal(b []byte) (Packet, bool) {
	var p Packet
	if len(b) < WireLen {
		return p, false
	}
	if b[0] != ReportID || b[1] != Magic {
		return p, false
	}
	p.Cmd = Cmd(b[2])
	p.Offset = uint16(b[3]) | uint16(b[4])<<8
	p.Length = b[5]
	p.Seq = b[6]
	copy(p.Data[:], b[9:WireLen])
	return p, true
}

// Payload 返回回包数据区里有效的前 n 字节。
func (p Packet) Payload(n int) []byte {
	if n < 0 || n > PayloadLen {
		n = PayloadLen
	}
	return p.Data[:n]
}

// SeqRing 分配 1..250 循环的序号，与固件的期望一致。
type SeqRing struct{ n byte }

// Next 返回下一个序号。
func (r *SeqRing) Next() byte {
	r.n = r.n%250 + 1
	return r.n
}
