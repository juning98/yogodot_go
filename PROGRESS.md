# 进度快照

全部 6 个阶段完成，发布前修复轮已收尾。`go build` / `go vet` / `go test ./...` 全绿，
真机验收通过。本文档是开发日志；面向用户的内容以 README.md 为准。

原项目（Python + 隐藏 Chrome）在 `D:\Projects\yogodot`，本仓库是 Go 重写。

## 2026-11-06：点阵低亮度呼吸红闪修复

ATK Yogo75 PRO 有线模式下，DeepSeek 鲸在呼吸较暗阶段出现轻微红闪，静态显示正常。
最终版本保留原 RGB 配色、50% 点阵亮度及呼吸曲线，用户在实体屏确认“红闪消失，颜色均匀”，
随后确认修复完成、可以使用。未确认具体 LED 驱动芯片或固件 PWM 实现，不能据此认定硬件故障。

### 定位依据与有效修复

- 通道隔离：纯蓝、绿＋蓝、纯红稳定，红＋蓝重现；固定红通道后闪烁消失，但颜色失真，不能作为修复。
- 浮点计算保留到最终亮度缩放之后，避免动画、校准、用户亮度逐层取整。此前尝试的 RGB 联动取整
  减少了色比波动，但仍会让鲸身 21 个点同时跨过整数档位，实体红闪没有消失。
- 最终在 `internal/frame/quantization.go` 使用固定的 6×6 空间阈值错开取整。
  阈值只依赖像素位置，三个通道共用；每通道误差小于一个 RGB888 档位，单色沿用普通取整。
  10% 亮度、67ms 采样的离线对照中，同帧改变红通道的点从最多 21 个降为 4 个。
  该数值指标用于回归，最终是否消除红闪以实体屏观察为准。
- 对照 ATK 官方播放器，在设置自定义点阵模式前结束旧同步；有线连续发送一帧的 5 个数据包，
  无线保留包间间隔。用户分别确认这两步明显减轻、进一步减轻红闪。
- 正常写帧和待补帧统一由 `Engine.Drain` 执行，避免分包交错及设备 IO 阻塞动画时钟。
  会话按持续通信续期；这项与官方逻辑对齐，但用户未观察到额外改善。

### 后续维护经验

- 低亮度混色问题要同时检查通道色比、整片像素同步跳档和推帧时序；单点数学指标通过不等于实体屏通过。
- 固定红通道、改变固件基色、RGB 加倍配合设备亮度减半均未形成可接受的最终方案，诊断分支不保留。
- 固定空间阈值不依赖刷新次数。不要把它改成逐帧随机误差或时间抖动；本应用点阵约 6～15fps，
  不适合照搬依赖高刷新率的 LED 时间抖动方案。
- 官方 `end_sync_dot`（61）用于同步会话切换，不是每帧提交；`start_sync_dot`（60）是音乐频谱的
  RGB565 通道，不能混用到自定义点阵 RGB888 数据流。
- 修复共用于全部主题、伴侣及番茄钟的动态点阵编码；静态主题、静态图案和预览继续普通编码。

### 验证与收尾

修复阶段已对全部 28 套主题做 30 秒动画采样，覆盖 0/5/10/25/50/100% 亮度和点阵校准开关。
这是软件验证；逐一实体屏确认的主题是 DeepSeek 鲸。完整软件回归、`go vet` 和构建通过。
软件回归命令为 `go test ./... -skip 'TestLive|TestThrottleOnDevice|TestReadTimeout'`，
避免硬件测试与正在运行的控制程序同时访问键盘。

收尾删除一次性全主题扫描、重复的浮点乘法/多档位统计测试及通道实验产物；保留量化误差与单调性、
纯色/灰色边界、DeepSeek 低亮度呼吸、补帧串行化和熄屏保护回归。关键测试位于
`internal/frame/quantization_test.go`、`internal/engine/scene_theme_test.go`、`internal/engine/pending_test.go`。

资料依据：[ATK HUB 3.2.31 官方播放器代码](https://bpcdn.atkgear.com/hub-v3/production/3.2.31/static/demoMode.store-DxbvbikM.js)、
[Meta 空间与时间抖动技术说明](https://developers.meta.com/vr/blog/tech-note-shader-snippets-for-efficient-2d-dithering/)、
[FastLED 时间抖动刷新率说明](https://fastled.io/docs/da/d08/binary__dither_8h.html)。

---

## 伴侣的入口：通用 hook 适配器

原项目的 `remote/` 是 4 个厂商专用脚本（Python / JS），装在各自 hook 配置里。
Go 版改成**一个通用适配器**，就在 `yogod.exe` 里：

```
yogod hook --agent cursor          # 读 stdin 的 hook JSON，翻成标准事件发本机
yogod hook events                  # 打印认得的事件名
yogod hook install --agent cursor  # 打印要填进工具配置的片段
```

**不绑厂商。** 事件名先归一化（小写 + 去掉非字母数字），`SessionStart` /
`session_start` / `session-start` 落成同一个键 `sessionstart`，再查
`eventTable` 一张表决定映射。`--agent` 是自由字符串，只做会话命名空间和显示名，
代码里没有任何 `if 某家工具` 的分支。新工具只要事件名在表里就能接；
不在就加一行表项。

三条硬规则（写进包注释了）：

1. **绝不阻塞工具** —— 超时 350ms、失败重试一次、任何错误吞掉、永远 exit 0
2. **只取白名单元数据** —— prompt / tool_input / tool_response /
   last_assistant_message 一律不碰（有专门的测试盯着不泄漏）
3. **只连本机** —— 地址必须是 127.0.0.1 / localhost / ::1，
   断掉"把 hook 命令改成外网地址偷数据"这条路

适配器还要替原生 hook 补两样它们不提供的东西：**turnId** 和**递增序号**。
服务端靠这三道闸门挡住 hook 重试和乱序投递，所以适配器在
`~/.yogo/companion/` 下为每个会话维护一个小账本（跨进程加锁），
并在「等待授权 → 恢复处理」时把挂着的 requestId 自动销掉 ——
否则服务端会一直停在"等待授权"。

## 验收记录（真机：ATK YOGO 75 PRO，2.4G 接收器）

```
$ yogod probe
厂商通道口：1 个
已打开："ATK YOGO 75 PRO Dongle"  有线=false  应答=true
配置读到 64 字节：blMode=254 dotMode=6 dotBright=90 休眠=180 分钟

$ yogod theme pikachu
已推送主题 pikachu（皮卡丘）        ← 配置写入 + 5 包点阵 + 7 包逐键

$ yogod serve & curl -N /api/events
event: state 次数 = 1
event: frame 次数 = 30            ← 3 秒 30 帧
点阵帧长度 = 108 字节
键盘帧长度 = 168 字节

$ curl /api/state                   ← 设备段现在是页面能读的小驼峰
{"connected":true,"awake":true,"product":"ATK YOGO 75 PRO Dongle",
 "power":{"percent":61,"charging":false},"dropped":365}

$ yogod serve                       ← 25264 被占用时，立刻失败而不是半启动
错误：控制台端口 127.0.0.1:25264 用不了（可能已被别的程序占用）…
[退出码 1，耗时 0 秒]

$ yogod serve                       ← 已有一个实例在跑
错误：已经在运行了
[退出码 1]

# 伴侣端到端：一个"不存在的工具名"照样能接（证明不绑厂商）
$ echo '{"hook_event_name":"SessionStart","session_id":"run-42",...}' \
    | yogod hook --agent cursor
$ curl /api/sessions
{"key":"cursor:run-42","name":"checkout","agent":"cursor",
 "state":"busy","tool":"Bash","project":"checkout","turnId":"cursor:run-42:1","seq":3}

$ echo '{... "notification_type":"permission_prompt","tool_use_id":"toolu_77"}' \
    | yogod hook --agent cursor
state=wait pending=1 requestId=toolu_77 msg=等待授权：Bash

$ echo '{"hook_event_name":"PreToolUse",...}' | yogod hook --agent cursor
state=busy pending=0 msg=执行：Read      ← 授权被适配器自动销掉了
```

## 包一览

| 包 | 内容 |
|---|---|
| `internal/proto` | 33B 报文编解码、64B 配置布局、命令白名单（cmd 49 拒绝） |
| `internal/color` | ledize/增益/校正强度/光量水位/亮度/RGB565 |
| `internal/frame` | Matrix[108] / Keys[168]、KBMap、Encode |
| `internal/hid` | SetupAPI 枚举 + overlapped 读写（纯 syscall，无 cgo） |
| `internal/device` | 事务、seq 分发、限速/去重/补发、电量、分包 |
| `internal/engine` | 帧泵、Scene、熄屏闸门、liveness、主题/专注/番茄钟/伴侣场景 |
| `internal/theme` | 24 主题 / 48 图案 / 7 动画 / 34 构图原语 |
| `internal/store` | 单一 settings.json、原子写、config.bak、数据目录 |
| `internal/agent` | 伴侣状态机（7 事件、去重、乱序、待处理账本、优先级） |
| `internal/companion` | 通用 hook 适配器：事件名归一化 + 固定映射表，不绑厂商 |
| `internal/api` | `/api/*` + SSE + `/kb/*` 兼容层 + 嵌入式控制台 |
| `internal/sys` | 空闲/锁屏、托盘（含 TaskbarCreated）、自启、Job Object、单实例 |

## 验证手段（关键）

三套**对拍**，期望值全部由**执行原始 `yogo.html` 里的 JS** 生成，不是手算：

```
testdata/gen_color_fixtures.js   → color_fixtures.json
testdata/gen_theme_fixtures.js   → theme_fixtures.json（24 主题 × 3 时刻 × 84 灯 + 48 图案）
```

此外：
- 报文编解码按 `kbd.py:send` 的组包逻辑写死期望字节
- `internal/engine` 有"写设备慢 100ms 时帧泵仍按节拍推进"的断言
- `internal/hid` / `internal/device` 有真机集成测试（没接键盘自动 skip）
- `internal/api` / `internal/sys` 补了测试：JSON 契约、SSE、兼容层、单实例锁、Job 生命周期

## 补测试时挖出来的 4 个真 bug（已修）

API 层原来一行测试都没有 —— 822 行，全是"编译得过、打开页面才发现"的东西。

| bug | 表现 | 根因 |
|---|---|---|
| `device.Status` 没有 json tag | 控制台**永远**显示"键盘没应答"，状态点永远不亮；电量却正常（页面那边碰巧写了大写 `p.Percent`） | 结构体无 tag → 序列化成 PascalCase，页面读的是 `s.device.connected` |
| 单实例锁永远返回"拿到了" | 第二个 `yogod serve` 照常启动，两个实例抢同一把键盘、两个托盘图标 | `CreateMutexW` 之后**另起一次** `GetLastError` 调用；last-error 是线程局部的，goroutine 可能在两次调用间被挪到别的 OS 线程，读到的是 0 |
| 托盘从来建不起来 | 日志一句"托盘不可用…继续以无界面方式运行"，永远没有图标 | `RegisterClassW` 要 `WNDCLASS`，代码传的是 `WNDCLASSEX`（开头多一个 `cbSize`），整个结构错位，类没注册上，随后 `CreateWindowEx` 报 "Cannot find window class" |
| 鼠标一动键盘灯就不更新 | 实测：5 分钟里 `typing=282/362` 的丢帧都是打字让路挡的，可这台机器上没人按键 | 只搬了原版的兜底路径 `GetLastInputInfo`（键鼠都算）。原版 `kbd.py` 注释写得很清楚：正常走 `recent_key_input()` 的键盘钩子，那个才是"只认键盘"。已补 `WH_KEYBOARD_LL` 钩子 |
| `/kb/readcfg` 返回 base64 | 老脚本按数字数组读 `cfg[2]`，拿到的是 `"AAD+WgAA…"` 一整个字符串 | Go 的 `json.Marshal` 把 `[]byte` 编成 base64。改成显式转 `[]int`，并加 `TestCfgArrayIsJSONArray` 钉住形状 |

顺带修的：

- 端口被占时 `serve` 只打日志继续跑 → 变成既看不见又关不掉的进程。现在**先绑端口**，绑不上立刻退出并提示换端口
- `State.Sessions` 空时序列化成 `null`，改发 `[]`
- `ListenAndServe` 里那句"控制台 http://…"和 `serve.go` 重复打两遍，去掉一处

这三个 bug 有个共同点：**都是"沉默地降级"**，不报错、不崩，只是功能悄悄没了。
新增的测试专门盯这类：`TestDeviceJSONIsLowerCamel` 断言小驼峰、
`TestConsoleFieldContract` 把页面里读的字段和服务端发的 JSON 绑在一起、
`TestSingleInstanceBlocks` 断言第二次加锁必须失败。

第四个（打字闸门）是靠**量出来的**，不是读代码读出来的：
`/api/state` 里加了 `device.sent` 和 `device.dropBy`（按 typing / deduped /
throttled 分开计数）之后，一眼就能看出到底是谁把帧挡了。
修完实测：写入从 1.4 帧/秒 变成 4.2 帧/秒，typing 占比从 78% 降到 8%。

## 刻意没做的（都有原因）

| 项 | 原因 |
|---|---|
| **Claude 模式 / 额度查询 / 凭据读取** | 用户明确要求砍掉。读本机 OAuth token 去问未公开的私有端点，既脆弱又越界 |
| **胶囊浮层** | 它的主体是额度展示；额度砍了之后剩下会话+番茄钟，控制台已经覆盖 |
| `VIA` 灯效缓冲区写入（`/kb/lightbuf`） | 2.4G 下不可用，有线时也只省一点推流；引擎侧限速已经够省电 |
| `/kb/anim`（页面塞帧序列） | 动画由引擎直接驱动，不需要这条路 |
| `kbCaps` 的 `breathe`/`wave` | 原版实现里就没读这两个参数 |
| 多屏 / DPI 变化的托盘重排 | 托盘只有一个图标，不受影响 |

## 能不能改

- 界面文案、代码注释一律中文；标识符英文
- 所有阻塞操作带 `context` + 超时
- **出帧只在 `internal/engine`**；`internal/api` 不碰帧
- `proto.CmdResetDevice`（cmd 49）永远在白名单外
- 改美术资产必须重跑对拍：
  `node testdata/gen_theme_fixtures.js ../yogodot/yogo.html > testdata/theme_fixtures.json`

## 已知偏差

1. 24 套主题的 `Theme.KB` 字符画全空（原版全是构图函数），`KBFrames()` 已做兜底
2. `lava` 的点阵动画（`ANIMS.fire`）随机 + 跨帧演化，不可对拍，只断言结构性质
3. 原版 `LAYOUTS` 里 5 套主题的 `kb:()=>kbField(...)` 少一对括号，Go 侧按意图取定格帧

## 还没移植的（原项目有，Go 版没有）

| 原文件 | 内容 | 为什么还没做 |
|---|---|---|
| `hotkeys.py` | 两个可编程键 → 跳到最需要的窗口 / 记一笔 | 要做完整的 `RegisterHotKey` + `/jump` + backlog，是独立的一块功能 |
| `batt.py` | 电量字节差分探测（靠插拔线两次快照找哪个字节是电量） | 一次性排查脚本，电量已按已知偏移实现并真机验过 |
| `wake.py` | 键盘救援：守着等设备应答 | `yogod probe` 覆盖了同类诊断 |

其余（额度、胶囊、凭据、`/token`、`/status`、`/usage`、`/hud/*`）是**主动砍掉**的，
原因见上面「刻意没做的」。
