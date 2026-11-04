package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wednesque/yogod/internal/companion"
)

// cmdHook 是 `yogod hook`：给 AI 编程工具的 hook 调用的适配器。
//
// 它必须在任何情况下都 exit 0 —— 这是挂在别人每回合流程里的进程，
// 报错退出会让用户的工具跟着报错。
func cmdHook(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "install":
			return cmdHookInstall(args[1:])
		case "events":
			return cmdHookEvents()
		}
	}

	cfg := companion.Config{}
	for i := 0; i < len(args); i++ {
		key := args[i]
		next := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch key {
		case "--agent", "--source":
			cfg.Agent = next()
		case "--event", "--hook-event-name":
			cfg.HookEvent = next()
		case "--url":
			cfg.URL = next()
		case "--state-dir":
			cfg.StateDir = next()
		case "--dry-run":
			cfg.DryRun = true
		case "--help", "-h":
			fmt.Fprint(os.Stderr, hookUsage)
			return nil
		default:
			// 不认识的参数直接忽略：宁可少一个选项，不能因为多一个就失败
		}
	}
	// 注意：这里**不能**把错误往上抛。Run 内部已经吞掉了发送失败，
	// 剩下能返回错误的只有状态文件读写，那也不该让工具挂掉。
	_ = companion.Run(cfg)
	return nil
}

const hookUsage = `yogod hook —— 通用 hook 适配器（按事件名，不按厂商）

  yogod hook --agent 我的工具                 读 stdin 的 hook JSON 并上报
  yogod hook --event Stop                     payload 不带事件名时从命令行给
  yogod hook --agent x --dry-run              只打印标准事件，不发送

  yogod hook events                           打印认得的全部事件名
  yogod hook install                          打印配置片段（默认挂全套事件）
  yogod hook install --events SessionStart,Stop
  yogod hook install --config <路径> --write  直接合并进配置文件（先备份）

事件名先归一化（转小写、去掉非字母数字）再查表，
所以 SessionStart / session_start / session-start 是同一个事件。
工具名只用来给会话分组，不参与映射 —— 任何工具都能接。

只连本机：目标地址必须是 127.0.0.1 / localhost / ::1。
默认 http://127.0.0.1:25264/events；用 --url 或环境变量 YOGO_PORT /
YOGO_COMPANION_URL 改地址。
`

// cmdHookEvents 打印事件名表。配 hook 的时候照着填。
func cmdHookEvents() error {
	known := companion.KnownEvents()
	names := make([]string, 0, len(known))
	for k := range known {
		names = append(names, k)
	}
	sort.Strings(names)

	fmt.Printf("%-22s %s\n", "归一化后的事件名", "映射到")
	fmt.Printf("%-22s %s\n", strings.Repeat("-", 20), strings.Repeat("-", 30))
	for _, n := range names {
		fmt.Printf("%-22s %s\n", n, known[n])
	}
	fmt.Printf("\n共 %d 个。配置里写原始写法（SessionStart）也可以，会先归一化再查表。\n", len(names))
	return nil
}

// ── 配置片段生成 ──────────────────────────────────────────────────

// defaultEvents 是"挂全套"时的默认事件列表。
// 用各家通用的驼峰写法；归一化之后 snake_case 也能命中。
var defaultEvents = []string{
	"SessionStart",
	"SessionEnd",
	"UserPromptSubmit",
	"PreToolUse",
	"PostToolUse",
	"PostToolUseFailure",
	"PermissionRequest",
	"Stop",
	"StopFailure",
	"Notification",
}

func cmdHookInstall(args []string) error {
	agent := ""
	configPath := ""
	events := defaultEvents
	write := false
	forceEvent := false

	for i := 0; i < len(args); i++ {
		next := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch args[i] {
		case "--agent", "--source":
			agent = next()
		case "--config":
			configPath = next()
		case "--events":
			if raw := next(); raw != "" {
				events = splitList(raw)
			}
		case "--write":
			write = true
		case "--force-event":
			forceEvent = true
		case "--help", "-h":
			fmt.Fprint(os.Stderr, hookUsage)
			return nil
		}
	}
	if len(events) == 0 {
		return fmt.Errorf("--events 是空的")
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("找不到自己的路径：%w", err)
	}
	exe = filepath.ToSlash(exe)

	snippet := buildHooksJSON(exe, agent, events, forceEvent)
	raw, err := json.MarshalIndent(snippet, "", "  ")
	if err != nil {
		return err
	}

	if !write {
		fmt.Printf("把这个合并进工具的 hook 配置：\n\n%s\n", raw)
		if configPath == "" {
			fmt.Printf("确定配置文件在哪之后，可以加 --config <路径> --write 让它自己合并。\n")
		} else {
			fmt.Printf("或者直接跑：yogod hook install --config %q --write\n", configPath)
		}
		return nil
	}
	if configPath == "" {
		return fmt.Errorf("--write 需要同时给 --config <配置文件路径>")
	}
	if err := mergeHooks(configPath, exe, agent, events, forceEvent); err != nil {
		return err
	}
	fmt.Printf("已合并进 %s\n（原文件备份在 %s.bak）\n", configPath, configPath)
	return nil
}

func splitList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// hookCommand 是写进配置的那条命令。
//
// 路径一定用双引号包住：Program Files 里有空格。
// 有些工具不把事件名放进 payload，那就只能从命令行传（--force-event）。
func hookCommand(exe, agent, event string, forceEvent bool) string {
	cmd := fmt.Sprintf("%q hook", exe)
	if agent != "" {
		cmd += " --agent " + agent
	}
	if forceEvent {
		cmd += " --event " + event
	}
	return cmd
}

func buildHooksJSON(exe, agent string, events []string, forceEvent bool) map[string]any {
	hooks := map[string]any{}
	for _, name := range events {
		hooks[name] = []any{
			map[string]any{
				"hooks": []any{
					map[string]any{
						"type":    "command",
						"command": hookCommand(exe, agent, name, forceEvent),
						"timeout": 8,
					},
				},
			},
		}
	}
	return map[string]any{"hooks": hooks}
}

// mergeHooks 把我们的 handler 合并进已有配置。
//
// 只做加法：已有的 hook 一个都不动，别的东西（permissions、model…）原样保留。
// 写之前先备份 —— 这个文件用户可能手改过很多次。
func mergeHooks(target, exe, agent string, events []string, forceEvent bool) error {
	doc := map[string]any{}
	raw, err := os.ReadFile(target)
	switch {
	case err == nil && len(raw) > 0:
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("%s 不是合法 JSON（为了不改坏它，这里停下）：%w", target, err)
		}
		if err := os.WriteFile(target+".bak", raw, 0o644); err != nil {
			return fmt.Errorf("备份失败，已中止：%w", err)
		}
	case os.IsNotExist(err):
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
	case err != nil:
		return err
	}

	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		doc["hooks"] = hooks
	}

	for _, name := range events {
		// 去重判据是"命令里含本 exe 路径" —— 第三参必须传 exe，
		// 传成配置文件路径会永远匹配不到,重复安装就叠加两条相同 hook。
		hooks[name] = appendHookEntry(hooks[name],
			hookCommand(exe, agent, name, forceEvent), exe)
	}

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(target, append(out, '\n'), 0o644)
}

// appendHookEntry 往某个事件的 matcher 列表里加一条，已经加过就原样返回。
func appendHookEntry(existing any, command, exe string) []any {
	list, _ := existing.([]any)
	for _, item := range list {
		m, _ := item.(map[string]any)
		for _, h := range asAnySlice(m["hooks"]) {
			hm, _ := h.(map[string]any)
			s, _ := hm["command"].(string)
			// 只认"指向本 exe 的 hook"，别把用户自己的 hook 当成已安装
			if strings.Contains(s, exe) && strings.Contains(s, "hook") {
				return list
			}
		}
	}
	return append(list, map[string]any{
		"hooks": []any{
			map[string]any{"type": "command", "command": command, "timeout": 8},
		},
	})
}

func asAnySlice(v any) []any {
	s, _ := v.([]any)
	return s
}
