//go:build windows

package hid

import (
	"context"
	"testing"
	"time"
)

// TestEnumerateSmoke 是本机冒烟测试：真的去枚举一次 HID 接口。
//
// 它验证的是最容易写错的那部分 —— SetupAPI 的结构体布局、
// SP_DEVICE_INTERFACE_DETAIL_DATA_W 的 cbSize（32/64 位不同）、
// 以及路径的 UTF-16 解码。这些东西写错了不会报错，只会枚举出 0 个设备。
func TestEnumerateSmoke(t *testing.T) {
	all, err := Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	// 任何一台正常的 Windows 机器都会有至少一个 HID 接口（键鼠）。
	if len(all) == 0 {
		t.Fatal("一个 HID 接口都没枚举到 —— struct 布局或 cbSize 很可能写错了")
	}

	var vendor int
	for _, it := range all {
		// 输入长度必须为正；输出长度为 0 是正常的 ——
		// 纯输入的 HID 集合（鼠标、键盘矩阵）本来就没有输出报告。
		if it.RxLen <= 0 {
			t.Errorf("输入报告长度不合法：%q rx=%d", it.Path, it.RxLen)
		}
		if it.Path == "" {
			t.Error("设备路径为空")
		}
		if it.VID == VID {
			vendor++
			t.Logf("ATK 设备: %04X:%04X page=%04X usage=%04X rx=%d tx=%d %q",
				it.VID, it.PID, it.UsagePage, it.Usage, it.RxLen, it.TxLen, it.Product)
		}
	}
	t.Logf("共枚举到 %d 个 HID 接口，其中 ATK（%04X）%d 个", len(all), VID, vendor)
}

// TestVendorInterfaceHasOutput 确认厂商口是可写的。
//
// 这一条很关键：FF60/61 上必须存在输出报告，否则 cmd 59/46 根本发不出去。
func TestVendorInterfaceHasOutput(t *testing.T) {
	v, err := VendorInterfaces()
	if err != nil {
		t.Fatalf("VendorInterfaces: %v", err)
	}
	if len(v) == 0 {
		t.Skip("没有接键盘/接收器，跳过")
	}
	for _, it := range v {
		if it.TxLen <= 0 {
			t.Errorf("厂商口没有输出报告，无法发送命令：%q", it.Path)
		}
		t.Logf("厂商口: %q rx=%d tx=%d 接收器=%v", it.Path, it.RxLen, it.TxLen, it.IsDongle())
	}
}

// TestVendorInterfaces 确认过滤逻辑不会把非 ATK 设备算进来。
func TestVendorInterfaces(t *testing.T) {
	v, err := VendorInterfaces()
	if err != nil {
		t.Fatalf("VendorInterfaces: %v", err)
	}
	for _, it := range v {
		if it.VID != VID {
			t.Fatalf("混进了非 ATK 设备：%04X", it.VID)
		}
		if !it.IsVendor() {
			t.Fatalf("混进了非厂商口：page=%04X usage=%04X", it.UsagePage, it.Usage)
		}
	}
	t.Logf("厂商通道口 %d 个（没有键盘时为 0）", len(v))
}

// TestOpenMissingPath 确认打开不存在的路径返回错误而不是崩。
func TestOpenMissingPath(t *testing.T) {
	_, err := Open(`\\?\hid#does-not-exist#1`, 33, 33)
	if err == nil {
		t.Fatal("打开不存在的设备应该失败")
	}
}

// TestReadTimeout 确认超时是可控的（不是卡死）。
//
// 没有键盘时拿不到 Link，所以这个测试只在有厂商口时才跑。
func TestReadTimeout(t *testing.T) {
	v, err := VendorInterfaces()
	if err != nil || len(v) == 0 {
		t.Skip("没有键盘/接收器，跳过")
	}
	l, err := Open(v[0].Path, v[0].RxLen, v[0].TxLen)
	if err != nil {
		t.Skipf("打不开厂商口：%v", err)
	}
	defer l.Close()

	start := time.Now()
	_, err = l.Read(context.Background(), 150*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Log("读到了数据（键盘在主动上报）")
		return
	}
	if err != ErrTimeout {
		t.Fatalf("期望 ErrTimeout，实际 %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("150ms 的超时实际等了 %s —— 超时机制没生效", elapsed)
	}
}
