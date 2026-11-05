package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/wednesque/yogod/internal/agent"
	"github.com/wednesque/yogod/internal/device"
	"github.com/wednesque/yogod/internal/engine"
	"github.com/wednesque/yogod/internal/proto"
	"github.com/wednesque/yogod/internal/store"
	"github.com/wednesque/yogod/internal/theme"
)

// 内嵌图标必须真的落到数据目录,且重复调用不重写（否则每次启动都写盘）。
func TestEnsureIcon(t *testing.T) {
	dir := t.TempDir()
	p := ensureIcon(dir)
	if p == "" {
		t.Fatal("ensureIcon 返回空路径,托盘又要没 logo 了")
	}
	b, err := os.ReadFile(p)
	if err != nil || len(b) != len(yogoICO) {
		t.Fatalf("图标落盘失败: %v", err)
	}
	// 已经一致时返回同一路径,不报错
	if p2 := ensureIcon(dir); p2 != p {
		t.Fatalf("二次调用路径变了: %q → %q", p, p2)
	}
}

// 没写过的目录退回默认地址;写过的目录读出写入值 ——
// 第二次双击能不能找到老实例的控制台,全看这一对。
func TestConsoleAddrRoundtrip(t *testing.T) {
	dir := t.TempDir()
	if got := readConsoleAddr(dir); got != defaultAddr() {
		t.Fatalf("空目录应退回默认地址, 得到 %q", got)
	}
	writeConsoleAddr(dir, "127.0.0.1:8899")
	if got := readConsoleAddr(dir); got != "127.0.0.1:8899" {
		t.Fatalf("写入后读回 %q", got)
	}
	// 文件内容被弄坏时也不能 panic,退回默认地址
	_ = os.WriteFile(filepath.Join(dir, "console.addr"), []byte("  \n"), 0o644)
	if got := readConsoleAddr(dir); got != defaultAddr() {
		t.Fatalf("坏文件应退回默认地址, 得到 %q", got)
	}
}

// 拓扑对比是集合语义：顺序变化不算变化，否则监视器会对着没变的拓扑空折腾。
func TestSameStringsSetSemantics(t *testing.T) {
	if !sameStrings([]string{"a", "b"}, []string{"b", "a"}) {
		t.Error("顺序不同应判相等")
	}
	if sameStrings([]string{"a", "b"}, []string{"a"}) {
		t.Error("数量不同应判不等")
	}
	if sameStrings([]string{"a", "a"}, []string{"a", "b"}) {
		t.Error("多重集不同应判不等")
	}
	if !sameStrings(nil, nil) {
		t.Error("空拓扑应判相等")
	}
}

// 对账的期望值计算：没选原厂灯效时是推流模式（254），
// 选了就是效果号 —— 算错了会和固件灯效打架。
func TestExpectedBLMode(t *testing.T) {
	if got := expectedBLMode(nil); got != proto.BLModeStream {
		t.Errorf("nil 场景期望 254，实际 %d", got)
	}
	sc := &engine.ThemeScene{}
	if got := expectedBLMode(sc); got != proto.BLModeStream {
		t.Errorf("无固件灯效期望 254，实际 %d", got)
	}
	eff, ok := device.EffectByID(1)
	if !ok {
		t.Skip("灯效表为空")
	}
	sc.KBFirmware = &eff
	if got := expectedBLMode(sc); got != byte(eff.ID) {
		t.Errorf("固件灯效期望 %d，实际 %d", eff.ID, got)
	}
}

// 伴侣场景下切背光来源必须重进伴侣：固件效果号 / 254 推流只在场景 Enter
// 写设备。不重进的话选原厂灯效后推流帧停了、设备还停在 254 等帧 ——
// 键盘冻在最后一帧（"伴侣下选原厂灯效卡死"的 bug）。
//
// 探针：设备不在时 Enter 必然失败，错误沿 switchScene → setKBEffect 报回来；
// 没触发重进的话全程不碰设备，返回 nil。
func TestSetKBEffectReentersCompanion(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{dir: dir, st: st}
	a.dev = &device.Controller{} // 不连接：任何配置写都会快速报未连接
	a.eng = engine.New(a.dev, engine.Options{})
	a.comp = &engine.CompanionScene{Store: agent.NewStore()}
	a.themeScene = a.newThemeScene(theme.Default().ID, true)
	a.comp.SetKeys(a.themeScene)

	eff, ok := device.EffectByID(1)
	if !ok {
		t.Skip("灯效表为空")
	}

	a.sceneID = "companion"
	if err := a.setKBEffect(eff.ID); err == nil {
		t.Fatal("伴侣下切原厂灯效没触发场景重进 —— 设备模式没写，键盘会冻在最后一帧")
	}
	// 设备写失败不该回滚用户的选择：设置已落库，链路恢复后会补上
	a.st.View(func(s *store.Settings) {
		if s.KBEffect == nil || *s.KBEffect != eff.ID {
			t.Fatalf("灯效选择应已保存，实际 %+v", s.KBEffect)
		}
	})

	// 专注的背光语义自管（全灭）：不切设备、不报错，下次进场景自然带上
	a.sceneID = "focus"
	if err := a.setKBEffect(-1); err != nil {
		t.Fatalf("专注下切灯效应只记账（下次进场景生效），报错 %v", err)
	}
	a.st.View(func(s *store.Settings) {
		if s.KBEffect != nil {
			t.Fatal("id=-1 应清掉灯效设置")
		}
	})
}

// 端口固定后不再顺延：被占就直接报错。
func TestBindConsoleFailsWhenTaken(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	if _, err := bindConsole(held.Addr().String()); err == nil {
		t.Error("端口被占时应报错，而不是顺延")
	}
}
