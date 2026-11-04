package device

import (
	"context"
	"errors"
	"testing"

	"github.com/wednesque/yogod/internal/hid"
)

// 已 Close 的控制器不允许 Reopen —— 否则退出路径会把设备又打开。
// 这个测试不需要真键盘：Close 在空控制器上是安全的，Reopen 必须先查 closed。
func TestReopenOnClosedController(t *testing.T) {
	c := &Controller{}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := c.Reopen(context.Background()); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("关闭后 Reopen 应返回 ErrNotConnected，实际 %v", err)
	}
}

// swap 失败（接口打不开）必须留下一个"无链路"的干净态：
// link 为 nil、awake 为 false —— 这样监视器下轮还会再试，
// 而不是捏着半装好的连接当成正常。
func TestSwapFailureLeavesControllerLinkless(t *testing.T) {
	c := &Controller{}
	err := c.swap(hid.Interface{Path: `\\?\yogod\not\a\real\device`}, false)
	if err == nil {
		t.Fatal("假路径上 swap 应失败")
	}
	if c.link != nil {
		t.Error("swap 失败后 link 应为 nil")
	}
	if c.Awake() {
		t.Error("swap 失败后 awake 应为 false")
	}
}
