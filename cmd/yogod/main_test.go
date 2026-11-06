package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/wednesque/yogod/internal/agent"
	"github.com/wednesque/yogod/internal/device"
	"github.com/wednesque/yogod/internal/engine"
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

// 场景配置只发布内存目标，设备不在线时依然能接受用户选择。
func TestSetKBEffectUpdatesCompanionIntent(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &app{st: st, dev: &device.Controller{}}
	a.eng = engine.New(a.dev, engine.Options{})
	a.comp = &engine.CompanionScene{Store: agent.NewStore()}
	a.themeScene = a.newThemeScene(theme.Default().ID, true)
	a.comp.SetKeys(a.themeScene)
	if err := a.switchScene("companion", a.comp); err != nil {
		t.Fatal(err)
	}
	if err := a.setKBEffect(1); err != nil {
		t.Fatal(err)
	}
	if got := a.eng.Scene().Mode(); got.Effect != 1 {
		t.Fatalf("mode %+v", got)
	}
	if a.eng.Scene() != a.comp {
		t.Fatal("switch effect replaced companion")
	}
	if err := a.setKBEffect(-1); err != nil {
		t.Fatal(err)
	}
	if got := a.eng.Scene().Mode(); got.Effect != -1 {
		t.Fatalf("mode %+v", got)
	}
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
