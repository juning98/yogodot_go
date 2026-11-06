// Package device 是键盘的逻辑设备层：协议事务、限速、去重、补发。
//
// 分工：
//
//	internal/hid     打开一个口，读字节、写字节（不懂协议）
//	internal/proto   报文长什么样、命令号是什么（纯函数）
//	internal/device  一条命令怎么发、多久发一次、发不出去怎么办（本包）
//	internal/engine  该显示什么（场景、动画、熄屏策略）
//
// 这一层是旧版 kbd.py 的对应物，但修掉了两个结构性问题：
//
//  1. 写操作返回 Result 而不是 void —— 上层能知道"到底发出去没有"，
//     再也不用猜"设备为什么不动"。
//  2. 限速只在这里，出帧者按自己的节拍出 —— 不再是"页面狂推、这里丢弃"。
package device

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wednesque/yogod/internal/frame"
	"github.com/wednesque/yogod/internal/hid"
	"github.com/wednesque/yogod/internal/proto"
)

// 时序参数。全部来自旧版 kbd.py 的实测值，不是拍脑袋定的。
const (
	// ReplyTimeout 是等一条命令回包的时间。2.4G 转发有延迟，键盘刚醒更慢。
	ReplyTimeout = 900 * time.Millisecond
	// HandshakeEvery 是会话空闲期限：持续通信会续期，空闲超时后重新握手。
	HandshakeEvery = 4 * time.Second
	// PowerEvery 是电量轮询间隔。电量变化慢，没必要频繁占用通道。
	PowerEvery = 30 * time.Second
	// PacketGap 是同一帧内包与包之间的空隙，别把 2.4G 链路的按键上报挤掉。
	PacketGap = 3 * time.Millisecond

	// MinMatrixGap / MinKeysGap 是默认档的帧间隔（点阵 6.25fps / 键灯 10fps）。
	// 它们存在的理由是 2.4G 链路上给按键上报留路 —— 不是省电：
	// 耗电大头是灯一直亮着，跟推帧频率无关。
	// 有线与 2.4G 实测（yogod bench）30fps 键灯都零失败，
	// 更高的档位见 RateTier，用户可在控制台切换。
	MinMatrixGap = 160 * time.Millisecond
	MinKeysGap   = 100 * time.Millisecond
	// SameMatrixHold 是"画面没变"时补推的时间（图在设备里不会自己丢）。
	SameMatrixHold = 30 * time.Minute
	// SameKeysHold 是键盘灯静态画面的补推间隔。
	SameKeysHold = 60 * time.Minute

	// ConnectTimeout 是单次打开口的超时。
	ConnectTimeout = 900 * time.Millisecond
)

// ErrNotConnected 表示设备不可用。
var ErrNotConnected = errors.New("device: 未连接")

// RateTier 是用户可选的推帧档位。
//
// 每一档都在真机上用 yogod bench 爬坡验证过（有线与 2.4G 各一遍）。
// 键灯 30fps 传输实测零失败，但 285 包/秒（15+30 双通道）在 2.4G 上
// 已实测会让打字发跳 —— 所以极速收到 15/25，流畅是不跳的日常档。
type RateTier int

const (
	// RateTierDefault 默认档：点阵 6.25fps / 键灯 10fps。
	// 与官方点阵满速流量同一量级，任何链路都稳。
	RateTierDefault RateTier = iota
	// RateTierSmooth 流畅档：点阵 10fps / 键灯 20fps。
	// 顺滑且远低于 2.4G 打字发跳的阈值，日常推荐。
	RateTierSmooth
	// RateTierExtreme 极速档：点阵 15fps / 键灯 25fps。
	// 顶着实用边界跑；2.4G 下若感觉打字发飘就降回流畅或开输入保护。
	RateTierExtreme
)

// Gaps 返回该档位对应的点阵/键灯最小帧间隔。
func (t RateTier) Gaps() (matrix, keys time.Duration) {
	switch t {
	case RateTierSmooth:
		return 100 * time.Millisecond, 50 * time.Millisecond // 10 / 20 fps
	case RateTierExtreme:
		return 67 * time.Millisecond, 40 * time.Millisecond // 15 / 25 fps
	default:
		return MinMatrixGap, MinKeysGap
	}
}

// SetRateTier 应用推帧档位。热切换链路后依然生效（swap 不重置非零值）。
func (c *Controller) SetRateTier(t RateTier) {
	m, k := t.Gaps()
	c.mu.Lock()
	c.minMatrixGap, c.minKeysGap = m, k
	c.mu.Unlock()
}

// RateGaps 返回当前生效的点阵/键灯帧间隔（诊断与测试用）。
func (c *Controller) RateGaps() (matrix, keys time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.minMatrixGap, c.minKeysGap
}

// Result 是一次写操作的结果。
//
// 有了它，上层不必再猜"为什么屏幕不动"：被限速、被去重、正在打字、
// 还是设备根本没答话，Reason 里写得很清楚。
type Result struct {
	// Sent 表示这一帧真的写进设备了。
	Sent bool
	// Reason 在 Sent 为 false 时说明原因：
	// "throttled" / "deduped" / "typing" / "disconnected"。
	// （"blanked"/"error" 是废弃草稿，实现从不产出这两种。）
	Reason string
}

// Sent 是一个成功结果的简写。
var okSent = Result{Sent: true}

// Power 是电量与供电状态。
//
// json tag 不能省：这个结构会原样进 /api/state，
// 少了 tag 就变成 PascalCase，页面上读到的是 undefined。
type Power struct {
	// Percent 是电量百分比。
	Percent int `json:"percent"`
	// Charging 表示正在充电。
	Charging bool `json:"charging"`
	// Wired 表示当前走的是数据线（不是 2.4G）。
	Wired bool `json:"wired"`
	// Flag 是设备报的原始供电标志，排查用。
	Flag int `json:"flag"`
}

// Status 是对外报告的设备状态（给 API / 日志 / 托盘用）。
type Status struct {
	Connected bool   `json:"connected"`
	Awake     bool   `json:"awake"`
	Product   string `json:"product"`
	Wired     bool   `json:"wired"`
	Error     string `json:"error"`
	Power     *Power `json:"power"`
	// Dropped 是被限速/去重挡下的帧数，纯统计。
	Dropped uint64 `json:"dropped"`
	// Sent 是真正写进设备的帧数（点阵 + 键盘各算一次）。
	// 它长时间不涨，就说明键盘没在更新 —— 这是"看着不动"时第一个该看的数。
	Sent uint64 `json:"sent"`
	// DropBy 按原因分开记被挡下的次数：
	// typing（打字让路）/ deduped（画面没变）/ throttled（限速）。
	// 光看总数分不清"键盘没动"到底是没内容还是被挡住。
	DropBy map[string]uint64 `json:"dropBy,omitempty"`
}

// Controller 拥有唯一一条到键盘的连接。
//
// 所有导出方法都是并发安全的。
type Controller struct {
	// Typing 由外部注入：最近 250ms 内有没有按键。
	// nil 表示不做打字让路。
	Typing func() bool

	mu   sync.Mutex // 串行化命令事务
	link *hid.Link
	info hid.Interface
	seq  proto.SeqRing

	pending  map[byte]chan []byte
	lastHand time.Time
	awake    bool
	lastErr  string
	haveCfg  bool
	lastCfg  proto.Config
	power    *Power
	powerAt  time.Time
	dropped  uint64
	sent     uint64
	dropBy   map[string]uint64
	closed   bool
	stopRead chan struct{}
	readDone chan struct{}

	// 上一帧内容与时间，用来做去重和限速。
	haveMatrix   bool
	lastMatrix   frame.Matrix
	lastMatrixAt time.Time
	// 被限速挡下、待补发的那一帧（只留最新的，画面是状态而不是队列）。
	matrixPending *frame.Matrix

	haveKeys    bool
	lastKeys    frame.Keys
	lastKeysAt  time.Time
	keysPending *frame.Keys

	// 限速帧间隔（用户档位，SetRateTier 调整；零值时 attach 补默认）。
	minMatrixGap time.Duration
	minKeysGap   time.Duration
}

// packetGap 是同一帧内包与包之间的空隙。
const packetGap = PacketGap

// Open 找到后面真的挂着键盘的那个厂商口。
//
// 插着数据线时会出现两个长得一模一样的 FF60/61，只有一个后面有键盘。
// 所以不能拿第一个，必须逐个握手，谁回包用谁；都不回包就占住一个等它醒。
func Open(ctx context.Context) (*Controller, error) {
	ifaces, err := hid.VendorInterfaces()
	if err != nil {
		return nil, err
	}
	if len(ifaces) == 0 {
		return nil, fmt.Errorf("device: 没找到键盘或接收器（VID %04X, FF60/61）", hid.VID)
	}

	var spare *Controller
	for _, it := range ifaces {
		c, err := openOne(ctx, it)
		if err != nil {
			continue
		}
		if c.ping(ctx) {
			c.awake = true
			return c, nil
		}
		if spare == nil {
			spare = c // 都不应答：留一个兜底，等键盘醒过来
		} else {
			c.Close()
		}
	}
	if spare != nil {
		spare.lastErr = "键盘没应答（关机 / 不在 2.4G 档 / 没电）"
		return spare, nil
	}
	return nil, fmt.Errorf("device: %d 个厂商口都打不开", len(ifaces))
}

func openOne(ctx context.Context, it hid.Interface) (*Controller, error) {
	c := &Controller{}
	if err := c.attach(it); err != nil {
		return nil, err
	}
	return c, nil
}

// attach 把控制器装到一个接口上：开句柄、起读循环。
//
// 字段写入全部在 mu 内一次完成：swap 是在控制器对外服务期间调用的，
// transact/readLoop 从别的 goroutine 并发读这些字段 —— 之前的"先写 link
// 再建 pending"中间窗会让回包投进旧 map，等待方 900ms 白等。
func (c *Controller) attach(it hid.Interface) error {
	l, err := hid.Open(it.Path, it.RxLen, it.TxLen)
	if err != nil {
		return err
	}
	info := it
	// 产品名带 "Dongle" 就是 2.4G 接收器；不带就是数据线直连
	if name := l.ProductName(); name != "" {
		info.Product = name
	}

	c.mu.Lock()
	c.link = l
	c.info = info
	c.pending = make(map[byte]chan []byte)
	c.stopRead = make(chan struct{})
	c.readDone = make(chan struct{})
	// 限速档只在零值时补默认：SetRateTier 设的档位要活过热切换（swap 也走这里）
	if c.minMatrixGap == 0 {
		c.minMatrixGap = MinMatrixGap
	}
	if c.minKeysGap == 0 {
		c.minKeysGap = MinKeysGap
	}
	done := c.readDone
	c.mu.Unlock()

	// 读循环在锁外启动：它第一件事就是捕获 link/stop，不能被锁卡住
	go c.readLoop(done)
	return nil
}

// Wired 报告当前是不是数据线直连。有线基本等于在充电。
func (c *Controller) Wired() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.info.IsDongle()
}

// Product 返回产品名。
func (c *Controller) Product() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.info.Product
}

// Connected 报告连接是否还在。
func (c *Controller) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.link != nil && !c.closed
}

// Awake 报告最近一次读配置有没有应答。
func (c *Controller) Awake() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.awake
}

// Status 汇总当前状态。
func (c *Controller) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Status{
		Connected: c.link != nil && !c.closed,
		Awake:     c.awake,
		Product:   c.info.Product,
		Wired:     !c.info.IsDongle(),
		Error:     c.lastErr,
		Power:     c.power,
		Dropped:   c.dropped,
		Sent:      atomic.LoadUint64(&c.sent),
		DropBy:    copyCounts(c.dropBy),
	}
}

// dropLocked 记一笔"这一帧没发出去"的账。调用方必须持有 c.mu。
func (c *Controller) dropLocked(reason string) {
	c.dropped++
	if c.dropBy == nil {
		c.dropBy = make(map[string]uint64, 4)
	}
	c.dropBy[reason]++
}

// copyCounts 复制一份计数，避免把内部 map 交给外部。
func copyCounts(src map[string]uint64) map[string]uint64 {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]uint64, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// Close 关闭连接。可重复调用。
func (c *Controller) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	l := c.link
	stop := c.stopRead
	done := c.readDone
	c.mu.Unlock()

	if l != nil {
		close(stop)
		_ = l.Close()
		<-done
	}
	return nil
}

// Path 返回当前接口的设备路径，用于拓扑对比。
func (c *Controller) Path() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.info.Path
}

// VendorPaths 列出系统当前的厂商口路径（拓扑快照，不碰设备）。
func VendorPaths() ([]string, error) {
	ifaces, err := hid.VendorInterfaces()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ifaces))
	for _, it := range ifaces {
		out = append(out, it.Path)
	}
	return out, nil
}

// Reopen 重新发现设备并热切换链路。
//
// 键盘在有线/2.4G 之间切换后，旧接口死掉、另一个接口后面才有键盘 ——
// 捏着旧句柄只会对着空气推帧。这里像 Open 一样逐个握手选活口：
//
//   - 找到应答的链路 → 换上去，返回 nil
//   - 旧口自己复活了 → 保留旧连接（同口恢复画面最连贯）
//   - 都没应答 → 旧口还在枚举里就保留旧连接（键盘可能只是睡了）；
//     旧口已消失就占住一个口，等键盘醒过来
func (c *Controller) Reopen(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrNotConnected
	}
	oldPath := c.info.Path
	c.mu.Unlock()

	ifaces, err := hid.VendorInterfaces()
	if err != nil {
		return err
	}
	if len(ifaces) == 0 {
		return fmt.Errorf("device: 没找到键盘或接收器（VID %04X, FF60/61）", hid.VID)
	}
	// 旧口还在就第一个试：同口恢复不用动任何状态
	for i := range ifaces {
		if ifaces[i].Path == oldPath {
			ifaces[0], ifaces[i] = ifaces[i], ifaces[0]
			break
		}
	}

	for _, it := range ifaces {
		n, err := openOne(ctx, it)
		if err != nil {
			continue
		}
		if !n.ping(ctx) {
			n.Close()
			continue
		}
		n.Close()
		if it.Path == oldPath {
			// 旧口复活：之前的失败是瞬时的，清掉错误继续用
			c.mu.Lock()
			c.awake = true
			c.lastErr = ""
			c.mu.Unlock()
			return nil
		}
		return c.swap(it, true)
	}

	// 没有活口：旧口还在枚举里就留着，键盘睡了比换错了强
	for _, it := range ifaces {
		if it.Path == oldPath {
			return errors.New("device: 键盘没应答（可能睡了），保留旧链路")
		}
	}
	// 旧口已从系统消失：占住一个口等键盘醒
	return c.swap(ifaces[0], false)
}

// swap 拆掉当前连接，把控制器装到新接口上。awake 传入这次切换后
// 设备是否应答应答（Reopen 选口时刚 ping 过）。
//
// 去重/限速/电量缓存全部作废 —— 新链路后面的固件状态不可信，
// 由上层 Invalidate 之后从零重推。
func (c *Controller) swap(it hid.Interface, awake bool) error {
	c.mu.Lock()
	old := c.link
	oldStop, oldDone := c.stopRead, c.readDone
	c.link = nil
	c.mu.Unlock()

	if old != nil {
		close(oldStop)
		_ = old.Close()
		<-oldDone
	}

	if err := c.attach(it); err != nil {
		c.mu.Lock()
		c.awake = false
		c.lastErr = "重连失败：" + err.Error()
		c.mu.Unlock()
		return err
	}

	c.mu.Lock()
	c.awake = awake
	if awake {
		c.lastErr = ""
	} else {
		c.lastErr = "键盘没应答（关机 / 不在 2.4G 档 / 没电）"
	}
	c.power, c.powerAt = nil, time.Time{}
	c.haveCfg = false
	c.haveMatrix, c.haveKeys = false, false
	c.matrixPending, c.keysPending = nil, nil
	c.lastMatrixAt, c.lastKeysAt = time.Time{}, time.Time{}
	c.lastHand = time.Time{}
	c.mu.Unlock()
	return nil
}

// ── 收 ────────────────────────────────────────────────────────────

// readLoop 一直读，按 seq 把回包投递给等待的命令。
//
// 一条 readLoop 终身服务一条 link（新 attach 会起新的循环），
// 所以 link/stop 在启动时捕获一次；pending map 则每次在锁内读字段 ——
// swap 会整体替换它。
func (c *Controller) readLoop(done chan struct{}) {
	defer close(done)
	c.mu.Lock()
	link := c.link
	stop := c.stopRead
	c.mu.Unlock()
	for {
		select {
		case <-stop:
			return
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		b, err := link.Read(ctx, time.Second)
		cancel()
		if err != nil {
			if errors.Is(err, hid.ErrTimeout) {
				continue // 超时是正常的：没命令时设备不回包
			}
			// 正常的拆链也会走到这（swap 先关 stop 再 Close），
			// 错误文案由 swap 覆盖，这里只管让循环退出。
			c.mu.Lock()
			c.lastErr = "读失败：" + err.Error()
			c.mu.Unlock()
			return
		}
		if len(b) < proto.HeaderLen {
			continue
		}
		p, ok := proto.Unmarshal(b)
		if !ok {
			continue
		}
		c.mu.Lock()
		ch, hit := c.pending[p.Seq]
		c.mu.Unlock()
		if hit {
			select {
			case ch <- p.Data[:]:
			default: // 已经有人收过了
			}
		}
	}
}

// ── 发 ────────────────────────────────────────────────────────────

// transact 发一条命令并等回包。超时返回 (nil, nil)；连接问题时返回 (nil, err)。
func (c *Controller) transact(ctx context.Context, pkt proto.Packet, wait bool) ([]byte, error) {
	c.mu.Lock()
	if c.link == nil || c.closed {
		c.mu.Unlock()
		return nil, ErrNotConnected
	}
	pkt.Seq = c.seq.Next()

	var ch chan []byte
	if wait {
		ch = make(chan []byte, 1)
		c.pending[pkt.Seq] = ch
	}
	link := c.link
	c.mu.Unlock()

	defer func() {
		if ch != nil {
			c.mu.Lock()
			delete(c.pending, pkt.Seq)
			c.mu.Unlock()
		}
	}()

	raw, err := pkt.Marshal()
	if err != nil {
		return nil, err
	}
	wctx, cancel := context.WithTimeout(ctx, ReplyTimeout)
	defer cancel()
	if err := link.Write(wctx, raw[:]); err != nil {
		c.mu.Lock()
		c.lastErr = "写失败：" + err.Error()
		c.mu.Unlock()
		return nil, fmt.Errorf("device: 写失败: %w", err)
	}
	if !wait {
		return nil, nil
	}

	timer := time.NewTimer(ReplyTimeout)
	defer timer.Stop()
	select {
	case b := <-ch:
		// 设备答话了，说明链路活着 —— 之前记的"写失败"之类错误作废。
		// 设备监视器靠这个区分"瞬时故障"和"链路真的死了"。
		c.mu.Lock()
		c.lastErr = ""
		c.mu.Unlock()
		return b, nil
	case <-timer.C:
		return nil, nil // 超时：设备可能睡着了，不算硬错误
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// handshake 在会话空闲超时后握手；持续通信续期，与官方 startCorrespond 一致。
func (c *Controller) handshake(ctx context.Context) {
	c.mu.Lock()
	now := time.Now()
	if now.Sub(c.lastHand) < HandshakeEvery {
		c.lastHand = now
		c.mu.Unlock()
		return
	}
	c.lastHand = now
	c.mu.Unlock()
	_, _ = c.transact(ctx, proto.New(proto.CmdHandshake, 0, nil), false)
}

// ping 握手 + 读一小段配置，判断这个口后面是不是真的有键盘。
func (c *Controller) ping(ctx context.Context) bool {
	c.mu.Lock()
	c.lastHand = time.Time{} // 强制握手
	c.mu.Unlock()
	c.handshake(ctx)
	b, err := c.transact(ctx, proto.New(proto.CmdReadConfig, 0, nil), true)
	return err == nil && b != nil
}

// EndDotSync 结束固件点阵同步会话。官方自定义播放在设置模式 6 前执行此操作。
func (c *Controller) EndDotSync(ctx context.Context) error {
	c.handshake(ctx)
	b, err := c.transact(ctx, proto.New(proto.CmdEndSyncDot, 0, nil), true)
	if err != nil {
		return err
	}
	if b == nil {
		return fmt.Errorf("device: 结束点阵同步超时")
	}
	return nil
}

// ReadConfig 读完整的 64 字节配置。
//
// 键盘刚醒时第一次多半没人应答，所以重试三次并强制重新握手。
func (c *Controller) ReadConfig(ctx context.Context) (proto.Config, error) {
	var cfg proto.Config
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(220 * time.Millisecond):
			case <-ctx.Done():
				return cfg, ctx.Err()
			}
			c.mu.Lock()
			c.lastHand = time.Time{} // 强制重握手
			c.mu.Unlock()
		}
		c.handshake(ctx)
		ok := true
		for _, ck := range proto.ConfigChunks {
			b, err := c.transact(ctx, proto.NewLen(
				proto.CmdReadConfig, ck.Offset, ck.Length, nil), true)
			if err != nil {
				return cfg, err
			}
			if len(b) < int(ck.Length) {
				ok = false
				lastErr = fmt.Errorf("device: 读配置超时（offset %d）", ck.Offset)
				break
			}
			copy(cfg[ck.Offset:], b[:ck.Length])
		}
		if ok {
			c.mu.Lock()
			c.awake = true
			c.lastErr = ""
			c.lastCfg = cfg
			c.haveCfg = true
			c.mu.Unlock()
			return cfg, nil
		}
	}
	c.mu.Lock()
	c.awake = false
	c.lastErr = "读配置超时"
	c.mu.Unlock()
	if lastErr == nil {
		lastErr = errors.New("device: 读配置超时")
	}
	return cfg, lastErr
}

// WriteConfig 写完整的 64 字节配置。
func (c *Controller) WriteConfig(ctx context.Context, cfg proto.Config) error {
	c.handshake(ctx)
	// 进/出同步会话前先收尾，官方也是这么做的
	_, _ = c.transact(ctx, proto.New(proto.CmdEndSyncLED, 0, nil), false)
	for _, ck := range proto.ConfigChunks {
		payload := cfg[ck.Offset : int(ck.Offset)+int(ck.Length)]
		if _, err := c.transact(ctx,
			proto.NewLen(proto.CmdWriteConfig, ck.Offset, ck.Length, payload), false); err != nil {
			return err
		}
	}
	c.mu.Lock()
	c.lastCfg = cfg
	c.haveCfg = true
	c.mu.Unlock()
	return nil
}

// PatchConfig 读-改-写，只动指定字节。
func (c *Controller) PatchConfig(ctx context.Context, patch proto.Patch) (proto.Config, error) {
	if err := proto.CheckPatch(patch); err != nil {
		return proto.Config{}, err
	}

	cfg, err := c.ReadConfig(ctx)
	if err != nil {
		return proto.Config{}, err
	}
	out := cfg.Patch(patch)
	if out == cfg {
		// 一个字节都没变就不写 —— 写 blMode 会重触发固件灯效（肉眼可见的闪），
		// 重进同一场景（伴侣 → 伴侣）不该有这种副作用。漂移照样会被修：
		// 上面是每次实读，配置不一致时这里一定不相等。
		return cfg, nil
	}
	if err := c.WriteConfig(ctx, out); err != nil {
		return proto.Config{}, err
	}
	return out, nil
}

// Config 返回最近一次读到的配置（没读过就是零值）。
func (c *Controller) Config() (proto.Config, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastCfg, c.haveCfg
}

// Power 读电量与供电状态。内部有 30 秒缓存。
func (c *Controller) Power(ctx context.Context, force bool) (*Power, error) {
	c.mu.Lock()
	if !force && c.power != nil && time.Since(c.powerAt) < PowerEvery {
		p := *c.power
		c.mu.Unlock()
		return &p, nil
	}
	c.mu.Unlock()

	c.handshake(ctx)
	b, err := c.transact(ctx, proto.NewLen(proto.CmdPowerInfo, 0, 0, nil), true)
	if err != nil {
		return nil, err
	}
	if len(b) < 2 {
		c.mu.Lock()
		p := c.power
		c.mu.Unlock()
		if p == nil {
			return nil, errors.New("device: 读不到电量")
		}
		return p, nil // 读不到就沿用上一次，别把界面清空
	}

	pct := int(b[0])
	if pct > 100 {
		pct = 100
	}
	flag := int(b[1])
	p := &Power{
		Percent: pct,
		Flag:    flag,
		Wired:   c.Wired(),
	}
	// 官方代码判 "== 2 才是充电"，但这把键盘实测：
	// 无线放电 = 0，插着线充电 = 1。所以按"非 0 且没充满"算，
	// 再拿"走的是有线口"兜底。不要再加"电量涨过就算充电"这类猜测。
	p.Charging = (flag != 0 || p.Wired) && pct < 100

	c.mu.Lock()
	c.power = p
	c.powerAt = time.Now()
	c.mu.Unlock()
	return p, nil
}

// ── 帧推送 ────────────────────────────────────────────────────────

// SendMatrix 推一帧点阵。108 字节 RGB888，分 5 包。
//
// 语义与旧版一致，但把"被挡下"的原因明确回给调用方：
//   - 被限速/去重/打字挡下 → 记成待补发，返回 Result{Sent:false}
//   - 真的写进设备 → Result{Sent:true}
//
// 不丢帧是有意的：动画丢一帧无所谓，但模式切换（比如切到专注要全黑）
// 也是一次推送，丢了就永远留在上一张图上 —— 表现成"点了没反应"。
func (c *Controller) SendMatrix(ctx context.Context, m frame.Matrix) (Result, error) {
	c.mu.Lock()
	if c.link == nil || c.closed {
		c.mu.Unlock()
		return Result{Reason: "disconnected"}, ErrNotConnected
	}
	now := time.Now()
	same := c.haveMatrix && c.lastMatrix == m
	gap := now.Sub(c.lastMatrixAt)
	hold := SameMatrixHold
	typing := c.Typing != nil && c.Typing()

	switch {
	case typing:
		c.matrixPending = &m
		c.dropLocked("typing")
		c.mu.Unlock()
		return Result{Reason: "typing"}, nil
	case same && gap < hold:
		c.matrixPending = nil
		c.dropLocked("deduped")
		c.mu.Unlock()
		return Result{Reason: "deduped"}, nil
	case gap < c.minMatrixGap:
		c.matrixPending = &m
		c.dropLocked("throttled")
		c.mu.Unlock()
		return Result{Reason: "throttled"}, nil
	}
	// 占住时间槽：并发进来的一律走待补发，不能各自再发一整帧
	c.lastMatrixAt = now
	c.lastMatrix = m
	c.haveMatrix = true
	c.matrixPending = nil
	c.mu.Unlock()

	return okSent, c.writeMatrix(ctx, m)
}

// writeMatrix 把一个 108 字节点阵帧拆包发出去。
//
// 分包细节（尤其是最后一片只有 12 字节有效）在 MatrixPackets 里，
// 那是纯函数、有单元测试 —— 旧版把它埋在循环里，最后一包算错就整块屏缺一角。
func (c *Controller) writeMatrix(ctx context.Context, m frame.Matrix) error {
	c.handshake(ctx)
	// 官方自定义点阵连续发送一帧的 5 个包；有线无需 2.4G 的包间让路。
	gap := packetGap
	if c.Wired() {
		gap = 0
	}
	for _, p := range MatrixPackets(m) {
		if _, err := c.transact(ctx, p, false); err != nil {
			return err
		}
		if gap > 0 {
			time.Sleep(gap)
		}
	}
	atomic.AddUint64(&c.sent, 1)
	return nil
}

// SendKeys 推一帧键盘灯。168 字节 RGB565 小端，分 7 包。
// 前提是背光模式已经是 254（否则固件不接推流）。
func (c *Controller) SendKeys(ctx context.Context, k frame.Keys) (Result, error) {
	c.mu.Lock()
	if c.link == nil || c.closed {
		c.mu.Unlock()
		return Result{Reason: "disconnected"}, ErrNotConnected
	}
	now := time.Now()
	same := c.haveKeys && c.lastKeys == k
	gap := now.Sub(c.lastKeysAt)
	typing := c.Typing != nil && c.Typing()

	switch {
	case typing:
		c.keysPending = &k
		c.dropLocked("typing")
		c.mu.Unlock()
		return Result{Reason: "typing"}, nil
	case same && gap < SameKeysHold:
		c.keysPending = nil
		c.dropLocked("deduped")
		c.mu.Unlock()
		return Result{Reason: "deduped"}, nil
	case gap < c.minKeysGap:
		c.keysPending = &k
		c.dropLocked("throttled")
		c.mu.Unlock()
		return Result{Reason: "throttled"}, nil
	}
	c.lastKeysAt = now
	c.lastKeys = k
	c.haveKeys = true
	c.keysPending = nil
	c.mu.Unlock()

	return okSent, c.writeKeys(ctx, k)
}

func (c *Controller) writeKeys(ctx context.Context, k frame.Keys) error {
	c.handshake(ctx)
	for _, p := range KeysPackets(k) {
		if _, err := c.transact(ctx, p, false); err != nil {
			return err
		}
		if packetGap > 0 {
			time.Sleep(packetGap)
		}
	}
	atomic.AddUint64(&c.sent, 1)
	return nil
}

// FlushPending 补发被限速/打字挡下的最新帧。
//
// 引擎按 30Hz 调用它即可 —— 内部自己判断间隔，没到点就直接返回。
func (c *Controller) FlushPending(ctx context.Context) {
	c.mu.Lock()
	mp, kp := c.matrixPending, c.keysPending
	now := time.Now()
	canM := mp != nil && now.Sub(c.lastMatrixAt) >= c.minMatrixGap
	canK := kp != nil && now.Sub(c.lastKeysAt) >= c.minKeysGap
	typing := c.Typing != nil && c.Typing()
	if typing {
		c.mu.Unlock()
		return // 打字优先，补发帧先等着
	}
	if canM {
		c.lastMatrixAt = now
		c.lastMatrix = *mp
		c.haveMatrix = true
		c.matrixPending = nil
	}
	if canK {
		c.lastKeysAt = now
		c.lastKeys = *kp
		c.haveKeys = true
		c.keysPending = nil
	}
	c.mu.Unlock()

	if canM {
		_ = c.writeMatrix(ctx, *mp)
	}
	if canK {
		_ = c.writeKeys(ctx, *kp)
	}
}

// ForgetKeys 背光交回固件（模式不是 254）时调用：忘掉手里那帧灯。
//
// 否则熄屏/恢复会把"关背光之前"的最后一帧主题配色推回去 ——
// 逐键推流不管背光模式是不是 0，都照亮。
func (c *Controller) ForgetKeys() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.haveKeys = false
	c.lastKeys = frame.Keys{}
	c.keysPending = nil
}

// Invalidateliveness 让"设备现在是什么模式"的确认作废。
//
// 引擎在链路事件、电量跳变、或回读对账不一致时调用它，
// 下一次 Enter 场景就会重新写配置、重推画面。
func (c *Controller) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.haveCfg = false
	c.lastMatrixAt = time.Time{}
	c.lastKeysAt = time.Time{}
	c.haveMatrix = false
	c.haveKeys = false
}
