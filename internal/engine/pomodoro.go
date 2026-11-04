package engine

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/wednesque/yogod/internal/color"
	"github.com/wednesque/yogod/internal/device"
	"github.com/wednesque/yogod/internal/frame"
	"github.com/wednesque/yogod/internal/proto"
)

func sin(x float64) float64  { return math.Sin(x) }
func absf(x float64) float64 { return math.Abs(x) }

// Phase 是番茄钟的阶段。
type Phase string

// 五个阶段。旧版还有 paused_f / paused_b，这里用布尔 paused 表达。
const (
	PhaseIdle       Phase = "idle"
	PhaseFocus      Phase = "focus"
	PhaseBreak      Phase = "break"
	PhaseAlert      Phase = "alert"      // 专注结束，闪
	PhaseBreakAlert Phase = "breakAlert" // 休息结束，闪
)

// Preset 是一组番茄钟参数（分钟）。
type Preset struct {
	Name      string `json:"name"`
	Focus     int    `json:"focus"`
	Break     int    `json:"break"`
	LongBreak int    `json:"longBreak"`
	Every     int    `json:"every"` // 每几个番茄来一次长休
}

// DefaultPresets 是内置的四组预设。
var DefaultPresets = []Preset{
	{Name: "25 / 5 / 15", Focus: 25, Break: 5, LongBreak: 15, Every: 4},
	{Name: "45 / 10 / 25", Focus: 45, Break: 10, LongBreak: 25, Every: 3},
	{Name: "15 / 3 / 10", Focus: 15, Break: 3, LongBreak: 10, Every: 4},
	{Name: "50 / 10 / 30", Focus: 50, Break: 10, LongBreak: 30, Every: 2},
}

// AlertFor 是到点后闪烁的时长。
const AlertFor = 30 * time.Second

// ring 是点阵屏外圈 20 格的顺时针路径（上边 → 右边 → 下边 → 左边）。
//
// 旧版是运行时算的；这里是常量，因为 6×6 是硬编码的硬件尺寸。
var ring = [20][2]int{
	{0, 0}, {0, 1}, {0, 2}, {0, 3}, {0, 4}, {0, 5},
	{1, 5}, {2, 5}, {3, 5}, {4, 5}, {5, 5},
	{5, 4}, {5, 3}, {5, 2}, {5, 1}, {5, 0},
	{4, 0}, {3, 0}, {2, 0}, {1, 0},
}

// tomatoArt 是到点时那只会跳的番茄。
var tomatoArt = [6]string{
	"..gg..", ".rrrr.", "rrrrrr", "rrrrrr", ".rrrr.", "..rr..",
}

// Pomodoro 是番茄钟。
//
// **时间一律以墙钟为准**（deadline 而不是 count--），所以机器睡眠、
// 进程被挂起之后再回来，剩余时间是准的。旧版按 tick 递减，
// 丢掉的秒永远补不回来 —— 这是它最明确的缺陷之一。
type Pomodoro struct {
	mu     sync.Mutex
	preset Preset
	phase  Phase
	paused bool

	// endsAt 是当前阶段的目标结束时刻；暂停时改用 remain。
	endsAt time.Time
	remain time.Duration
	// alertUntil 是"闪烁提醒"的截止时刻。
	alertUntil time.Time

	completed int // 今天完成了几个
}

// NewPomodoro 建一个番茄钟。
func NewPomodoro(p Preset) *Pomodoro {
	return &Pomodoro{preset: p, phase: PhaseIdle}
}

// Preset 返回当前预设。
func (p *Pomodoro) Preset() Preset {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.preset
}

// SetPreset 换一组参数。空闲时立即生效，运行中则从下一个阶段开始生效。
func (p *Pomodoro) SetPreset(pr Preset) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.preset = pr
	if p.phase == PhaseIdle {
		p.remain = time.Duration(pr.Focus) * time.Minute
	}
}

// Start 开始一段专注。已经在专注/休息就什么都不做。
func (p *Pomodoro) Start(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.phase == PhaseFocus || p.phase == PhaseBreak {
		return
	}
	p.phase = PhaseFocus
	p.paused = false
	p.endsAt = now.Add(time.Duration(p.preset.Focus) * time.Minute)
}

// Pause 暂停 / 继续。
func (p *Pomodoro) Pause(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.phase != PhaseFocus && p.phase != PhaseBreak {
		return
	}
	if p.paused {
		p.endsAt = now.Add(p.remain)
		p.paused = false
		return
	}
	p.remain = maxDur(0, p.endsAt.Sub(now))
	p.paused = true
}

// Skip 跳到下一阶段（专注→休息，休息→专注）。
func (p *Pomodoro) Skip(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.advance(now)
}

// Reset 回到空闲。
func (p *Pomodoro) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.phase = PhaseIdle
	p.paused = false
	p.endsAt = time.Time{}
	p.alertUntil = time.Time{}
	p.remain = time.Duration(p.preset.Focus) * time.Minute
}

// remainingLocked 返回当前阶段还剩多久。
func (p *Pomodoro) remainingLocked(now time.Time) time.Duration {
	switch p.phase {
	case PhaseFocus, PhaseBreak:
		if p.paused {
			return maxDur(0, p.remain)
		}
		return maxDur(0, p.endsAt.Sub(now))
	case PhaseAlert, PhaseBreakAlert:
		return maxDur(0, p.alertUntil.Sub(now))
	default:
		return time.Duration(p.preset.Focus) * time.Minute
	}
}

// totalLocked 返回当前阶段的总时长。
func (p *Pomodoro) totalLocked() time.Duration {
	switch p.phase {
	case PhaseBreak:
		return time.Duration(p.nextBreakMinutesLocked()) * time.Minute
	case PhaseAlert, PhaseBreakAlert:
		return AlertFor
	default:
		return time.Duration(p.preset.Focus) * time.Minute
	}
}

func (p *Pomodoro) nextBreakMinutesLocked() int {
	every := p.preset.Every
	if every <= 0 {
		every = 4
	}
	if p.completed > 0 && p.completed%every == 0 && p.preset.LongBreak > 0 {
		return p.preset.LongBreak
	}
	return p.preset.Break
}

// advance 进入下一阶段。调用方必须持锁。
func (p *Pomodoro) advance(now time.Time) {
	switch p.phase {
	case PhaseFocus:
		p.completed++
		p.phase = PhaseAlert
		p.alertUntil = now.Add(AlertFor)
	case PhaseAlert:
		p.phase = PhaseBreak
		p.paused = false
		p.endsAt = now.Add(time.Duration(p.nextBreakMinutesLocked()) * time.Minute)
	case PhaseBreak:
		p.phase = PhaseBreakAlert
		p.alertUntil = now.Add(AlertFor)
	case PhaseBreakAlert:
		p.phase = PhaseFocus
		p.paused = false
		p.endsAt = now.Add(time.Duration(p.preset.Focus) * time.Minute)
	default:
		p.phase = PhaseFocus
		p.paused = false
		p.endsAt = now.Add(time.Duration(p.preset.Focus) * time.Minute)
	}
}

// Advance 检查是否该进入下一阶段。由外部 1 秒 ticker 调用。
//
// 返回 true 表示阶段刚变化（调用方可能想切场景、发通知）。
func (p *Pomodoro) Advance(now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.phase == PhaseIdle || p.paused {
		return false
	}
	rem := p.remainingLocked(now)
	if rem > 0 {
		return false
	}
	p.advance(now)
	return true
}

// Status 是对外报告的状态。
type PomStatus struct {
	Phase     Phase         `json:"phase"`
	Paused    bool          `json:"paused"`
	Remaining time.Duration `json:"remainingMs"`
	Total     time.Duration `json:"totalMs"`
	Completed int           `json:"completed"`
	Preset    Preset        `json:"preset"`
}

// Status 返回当前状态。
func (p *Pomodoro) Status(now time.Time) PomStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return PomStatus{
		Phase:     p.phase,
		Paused:    p.paused,
		Remaining: p.remainingLocked(now),
		Total:     p.totalLocked(),
		Completed: p.completed,
		Preset:    p.preset,
	}
}

// IsAlerting 报告现在是不是"到点闪烁"阶段。
func (p *Pomodoro) IsAlerting() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.phase == PhaseAlert || p.phase == PhaseBreakAlert
}

// ── Scene 实现 ────────────────────────────────────────────────────

// Name 实现 Scene。
func (p *Pomodoro) Name() string { return "pomodoro" }

// Frame 实现 Scene。
func (p *Pomodoro) Frame(t time.Duration) Render {
	p.mu.Lock()
	now := time.Now()
	phase := p.phase
	rem := p.remainingLocked(now)
	total := p.totalLocked()
	p.mu.Unlock()

	sec := t.Seconds()
	progress := 0.0
	if total > 0 {
		progress = 1 - float64(rem)/float64(total)
	}
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}

	var g frame.Grid
	var l frame.LEDs

	switch phase {
	case PhaseAlert, PhaseBreakAlert:
		g = tomatoFrame(sec)
		l = alertKeys(sec, phase)
	default:
		g = ringFrame(sec, progress, phase == PhaseBreak)
		l = barKeys(progress, phase == PhaseBreak)
	}

	m := g.Encode(color.DefaultCalibration(), 50, false)
	k := l.Encode(color.DefaultCalibration(), 70)
	// 预览帧直通原色：校准和亮度都是灯珠的电气补偿，页面显示原始颜色。
	pm := g.Encode(color.DefaultCalibration(), 100, false)
	pk := frame.RawKeys(l, 100)
	return Render{Matrix: &m, Keys: &k, PrevMatrix: &pm, PrevKeys: &pk}
}

// Enter 实现 Scene：番茄钟要同时接管点阵和键盘。
func (p *Pomodoro) Enter(ctx context.Context, ctrl *device.Controller) error {
	patch := proto.Patch{
		proto.OffBLMode:     proto.BLModeStream,
		proto.OffBLBright:   90,
		proto.OffBLColorful: 0,
	}
	for k, v := range proto.DotModePatch(proto.DotModeCustom, u8(90), u8(3), nil, nil, nil) {
		patch[k] = v
	}
	_, err := ctrl.PatchConfig(ctx, patch)
	return err
}

// Exit 实现 Scene。
func (p *Pomodoro) Exit(context.Context, *device.Controller) {}

// ringFrame 画进度环：外圈 20 格随进度点亮，中间一块随呼吸明暗。
func ringFrame(t, progress float64, isBreak bool) frame.Grid {
	var g frame.Grid
	base := color.RGB{255, 120, 0}
	if isBreak {
		base = color.RGB{40, 225, 80}
	} else if progress > 0.8 {
		// 最后 20% 从橙推向红
		base = color.Mix(color.RGB{255, 120, 0}, color.RGB{255, 20, 20}, (progress-0.8)/0.2)
	}

	lit := int(progress*float64(len(ring)) + 0.5)
	for i, rc := range ring {
		if i < lit {
			g[rc[0]][rc[1]] = base
		} else {
			g[rc[0]][rc[1]] = color.RGB{12, 9, 16}
		}
	}
	// 中心呼吸
	p := 0.35 + 0.65*absf(sin(t*1.6))
	center := color.Scale(base, p*0.9)
	for _, rc := range [][2]int{{2, 2}, {2, 3}, {3, 2}, {3, 3}} {
		g[rc[0]][rc[1]] = center
	}
	return g
}

// tomatoFrame 画到点提醒：番茄一边跳一边闪。
func tomatoFrame(t float64) frame.Grid {
	b := 0.45 + 0.55*absf(sin(t*5.2))
	jump := 0
	if sin(t*4.1) > 0.55 {
		jump = 1
	}
	var g frame.Grid
	for r, row := range tomatoArt {
		for c := 0; c < len(row); c++ {
			rr := r - jump
			if rr < 0 {
				continue
			}
			switch row[c] {
			case 'g':
				g[rr][c] = color.RGB{color.Clamp(60 * b), color.Clamp(235 * b), color.Clamp(50 * b)}
			case 'r':
				g[rr][c] = color.RGB{color.Clamp(255 * b), color.Clamp(45 * b), color.Clamp(30 * b)}
			}
		}
	}
	return g
}

// barKeys 画键盘进度条：16 列从左往右填满。
func barKeys(progress float64, isBreak bool) frame.LEDs {
	col := color.RGB{255, 120, 0}
	dim := color.RGB{40, 14, 0}
	if isBreak {
		col = color.RGB{40, 225, 80}
		dim = color.RGB{0, 40, 18}
	} else if progress > 0.8 {
		col = color.Mix(color.RGB{255, 120, 0}, color.RGB{255, 20, 20}, (progress-0.8)/0.2)
	}

	var l frame.LEDs
	frame.EachCell(func(r, c, led int) {
		x := (float64(c) + 0.5) / float64(frame.KBCols)
		k := (progress - x) * 9
		k = k + 0.5
		if k < 0 {
			k = 0
		}
		if k > 1 {
			k = 1
		}
		l[led] = color.Mix(dim, col, color.Smoothstep(k))
	})
	return l
}

// alertKeys 画到点提醒：整块键盘一起脉动。
func alertKeys(t float64, phase Phase) frame.LEDs {
	b := 0.35 + 0.65*absf(sin(t*5.2))
	col := color.RGB{255, 60, 0}
	if phase == PhaseBreakAlert {
		col = color.RGB{40, 225, 80}
	}
	c := color.Scale(col, b)
	var l frame.LEDs
	l.Fill(c)
	return l
}

func maxDur(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
