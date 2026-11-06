package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/wednesque/yogod/internal/frame"
	"github.com/wednesque/yogod/internal/proto"
	"github.com/wednesque/yogod/internal/store"
)

// kbCtx 给兼容层里的一次设备操作一个上限。
//
// 旧版这里没有超时，一次慢操作就能把 HTTP 线程和设备锁一起挂住 ——
// 那正是"跑久了卡死"的起点之一。
func kbCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 8*time.Second)
}

// registerCompat 注册旧版的 /kb/* 接口。
//
// 这一层存在的意义是"让老脚本和老页面还能用"，不是主路径。
// 控制台早就改成了 /api/*，页面不再出帧。
func (s *Server) registerCompat() {
	for _, p := range []string{
		"/kb/status", "/kb/reconnect", "/kb/readcfg", "/kb/config",
		"/kb/dotmode", "/kb/matrix", "/kb/keys", "/kb/stopkeys", "/kb/power",
		"/kb/lightbuf", "/kb/anim", "/kb/anim/stop",
		"/events", "/sessions/clear", "/snapshot",
	} {
		path := p
		s.mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			s.handleCompat(w, r, path)
		})
	}
}

func (s *Server) handleCompat(w http.ResponseWriter, r *http.Request, path string) {
	dev := s.d.Device

	switch path {
	case "/events":
		s.handleEvents(w, r)
		return
	case "/sessions/clear":
		s.handleSessions(w, r)
		return
	case "/snapshot":
		writeJSON(w, http.StatusOK, s.d.Agent.Snapshot())
		return
	}

	if r.Method == http.MethodGet {
		switch path {
		case "/kb/status", "/kb/power":
		default:
			http.Error(w, "只支持 POST", http.StatusMethodNotAllowed)
			return
		}
	}

	var body map[string]any
	if r.Method == http.MethodPost {
		_ = decodeLoose(r, &body)
	}
	get := func(k string) any {
		if body == nil {
			return nil
		}
		return body[k]
	}

	ctx, cancel := kbCtx()
	defer cancel()

	switch path {
	case "/kb/status":
		st := dev.Status()
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "connected": st.Connected, "awake": st.Awake,
			"product": st.Product, "wired": st.Wired, "err": st.Error,
			"power":   st.Power,
			"blanked": false, "locked": false, "idle": 0,
			"via": false, "anim": false,
		})

	case "/kb/power":
		p, err := dev.Power(ctx, false)
		if err != nil || p == nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok": false, "awake": dev.Awake(), "why": "读不到电量",
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "awake": true, "pct": p.Percent,
			"charging": p.Charging, "wired": p.Wired, "flag": p.Flag,
		})

	case "/kb/reconnect":
		cfg, err := dev.ReadConfig(ctx)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok": false, "awake": false, "why": err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "awake": true, "cfg": cfgArray(cfg),
		})

	case "/kb/readcfg":
		cfg, err := dev.ReadConfig(ctx)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "awake": false, "why": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "awake": true, "cfg": cfgArray(cfg)})

	case "/kb/config":
		raw, _ := get("bytes").(map[string]any)
		if len(raw) == 0 {
			http.Error(w, "缺少 bytes", http.StatusBadRequest)
			return
		}
		patch := proto.Patch{}
		for k, v := range raw {
			var off int
			for _, ch := range k {
				if ch < '0' || ch > '9' {
					off = -1
					break
				}
				off = off*10 + int(ch-'0')
			}
			if off < 0 || off >= proto.ConfigLen {
				http.Error(w, "配置偏移越界", http.StatusBadRequest)
				return
			}
			patch[off] = byte(toInt(v))
		}
		cfg, err := dev.PatchConfig(ctx, patch)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "awake": dev.Awake(), "why": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "awake": true, "cfg": cfgArray(cfg)})

	case "/kb/dotmode":
		b := func(k string) *byte {
			if v := get(k); v != nil {
				n := byte(toInt(v))
				return &n
			}
			return nil
		}
		var color *[3]byte
		if arr, ok := get("color").([]any); ok && len(arr) >= 3 {
			color = &[3]byte{byte(toInt(arr[0])), byte(toInt(arr[1])), byte(toInt(arr[2]))}
		}
		patch := proto.DotModePatch(
			byte(toInt(get("mode"))), b("brightness"), b("speed"),
			nil, b("colorIndex"), color)
		cfg, err := dev.PatchConfig(ctx, patch)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "awake": dev.Awake(), "why": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "awake": true, "cfg": cfgArray(cfg)})

	case "/kb/matrix":
		if !s.d.Engine.Enabled().Matrix {
			http.Error(w, "点阵已关闭", 409)
			return
		}
		arr, ok := get("rgb").([]any)
		if !ok || len(arr) < frame.MatrixLen {
			http.Error(w, "rgb 需要 108 个数", http.StatusBadRequest)
			return
		}
		var m frame.Matrix
		for i := 0; i < frame.MatrixLen; i++ {
			m[i] = byte(toInt(arr[i]))
		}
		err := dev.SendMatrix(ctx, m)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": err == nil, "awake": dev.Awake(), "sent": err == nil, "why": errorText(err),
		})

	case "/kb/keys":
		if !s.d.Engine.Enabled().Keys {
			http.Error(w, "背光已关闭", 409)
			return
		}
		arr, ok := get("rgb565").([]any)
		if !ok || len(arr) < frame.KeysLen {
			http.Error(w, "rgb565 需要 168 个数", http.StatusBadRequest)
			return
		}
		var k frame.Keys
		for i := 0; i < frame.KeysLen; i++ {
			k[i] = byte(toInt(arr[i]))
		}
		err := dev.SendKeys(ctx, k)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": err == nil, "awake": dev.Awake(), "sent": err == nil, "why": errorText(err),
		})

	case "/kb/stopkeys":
		s.d.Settings.Update(func(c *store.Settings) { c.Outputs.Keys = false })
		if err := s.d.Save(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if s.d.ApplyOutput != nil {
			s.d.ApplyOutput()
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case "/kb/lightbuf", "/kb/anim", "/kb/anim/stop":
		// VIA 灯效缓冲区与原生帧序列播放器没有移植：
		// 现在动画由引擎产出，不再需要"页面把帧序列塞给设备"这条路。
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "why": "Go 版未实现：动画由 internal/engine 直接驱动",
		})

	default:
		http.NotFound(w, r)
	}
}

// cfgArray 把 64 字节配置编成 JSON 数字数组。
//
// 不能直接塞 cfg[:] —— Go 会把 []byte 编码成 base64 字符串，
// 而老脚本读的是 [0,0,254,90,...] 这种数字数组，形状必须一致。
func cfgArray(cfg proto.Config) []int {
	out := make([]int, len(cfg))
	for i, b := range cfg {
		out[i] = int(b)
	}
	return out
}

func toInt(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int64:
		return t
	case int:
		return int64(t)
	case string:
		var n int64
		for _, ch := range t {
			if ch < '0' || ch > '9' {
				return 0
			}
			n = n*10 + int64(ch-'0')
		}
		return n
	}
	return 0
}

// decodeLoose 尽力解析请求体；失败不报错（兼容层里有些调用方发的不是严格 JSON）。
func decodeLoose(r *http.Request, v *map[string]any) error {
	if r.Body == nil {
		return nil
	}
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	return dec.Decode(v)
}

func errorText(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}
