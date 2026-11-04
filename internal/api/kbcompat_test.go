package api

import (
	"encoding/json"
	"testing"

	"github.com/wednesque/yogod/internal/proto"
)

// TestCfgArrayIsJSONArray 盯的是兼容层的形状。
//
// 直接把 proto.Config（[64]byte）塞进 JSON 会被编成 base64 字符串，
// 而老脚本读的是 [0,0,254,90,...]。这个差别编译期看不出来，
// 只有真去解析才会炸 —— 所以单独钉一个测试。
func TestCfgArrayIsJSONArray(t *testing.T) {
	var cfg proto.Config
	cfg[proto.OffBLMode] = proto.BLModeStream
	cfg[proto.OffBLBright] = 90

	raw, err := json.Marshal(cfgArray(cfg))
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	if len(raw) == 0 || raw[0] != '[' {
		t.Fatalf("不是 JSON 数组，而是：%s", string(raw[:min(len(raw), 40)]))
	}

	var back []int
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("解不回数组：%v", err)
	}
	if len(back) != proto.ConfigLen {
		t.Fatalf("长度 = %d，应为 %d", len(back), proto.ConfigLen)
	}
	if back[proto.OffBLMode] != int(proto.BLModeStream) {
		t.Errorf("blMode = %d", back[proto.OffBLMode])
	}
	if back[proto.OffBLBright] != 90 {
		t.Errorf("blBright = %d", back[proto.OffBLBright])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
