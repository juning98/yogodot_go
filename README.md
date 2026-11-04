# yogod-go

ATK YOGO 75 PRO 点阵屏与键盘背光控制 —— Go 重写版。

本仓库是 [yogodot](https://github.com/Wednesque/yogodot)（Python + 浏览器页面引擎）的
Go 重写：把原项目藏在 Chrome 窗口里的 JS 动画引擎搬进进程内，用 goroutine 帧泵直接驱动键盘。
主题、图案、调色板、动画曲线全部逐位对拍移植 —— 原项目 24 套主题在 Go 侧的颜色输出
与原 JS 逐字节一致（见「测试」一节的对拍说明）。

```
原项目: Chrome 页面里的 JS 定时器 ──HTTP──▶ Python ──HID──▶ 键盘
本仓库: goroutine 帧泵 ────1 槽邮箱────▶ 写设备 goroutine ──HID──▶ 键盘
```

## 功能

- **24 套主题 / 48 张图案**：皮卡丘、吃豆人、哆啦A梦、初音、EVA、我的世界……
  逐键配色 + 6×6 点阵成套切换，键盘 ← → 键可快速"试穿"。
- **Coding 伴侣**：把 AI 编程工具的任务状态显示在点阵屏上 ——
  形状认工具、颜色认状态、节奏认进展。内置 Claude Code / Codex / OpenCode 三张脸，
  其余工具共用一颗宝石兜底，**任何客户端接进来就有完整灯语**。
  接入是一个通用 hook 适配器，不绑厂商（见「Coding 伴侣」一节）。
- **20 个原厂键盘灯效**：背光交给键盘自己的芯片跑，主机一帧不推，最省电。
- **番茄钟 / 专注**：番茄钟走完自动把场景切过去提醒；专注模式全灭，退出恢复原样。
- **系统托盘 + 开机自启**：双击常驻，再双击唤醒控制台；单实例互斥。
- **本地控制台**（embed 单文件，无需联网）：主题/图案/场景/灯效/亮度/校准/
  推帧档位/熄屏策略，全部实时生效；预览走 SSE 推送真实帧，所见即设备所收。
- **链路自愈**：键盘休眠、被官方 HUB 抢、有线/2.4G 热切换，都能自己恢复。

要求：Windows，键盘以**数据线或 2.4G 接收器**连接（走 vendor HID 接口，蓝牙不支持）。

## 快速开始

1. 从 [Releases](../../releases) 下载 `yogod-v<版本>-windows-amd64.zip`，解压到任意目录 —— 免安装，
   单个 exe 内嵌全部资源（控制台、图标），不需要 Python 或任何运行时。
2. 双击 `yogod.exe` —— 托盘出现图标，自动打开控制台（http://127.0.0.1:25264）。
3. 在「主题」页点一套喜欢的，键盘立刻换装。

命令行等价物：

```bash
yogod serve                 # 常驻：托盘 + 控制台 + 帧引擎（双击等效）
yogod serve 127.0.0.1:9000  # 临时换端口
```

端口固定 25264，被占用会直接报错而不是顺延 —— 端口一换，hook 和页面都得跟着找，
反而更难排查。确需换端口：

```powershell
$env:YOGO_PORT='8899'; yogod serve
```

数据目录是 `%LOCALAPPDATA%\yogod`（不会往下载目录里撒配置文件）。
想要**便携模式**（配置/日志全程跟着 exe 走）：在 exe 旁边放一个名为
`portable` 的空文件再运行即可；exe 旁边已有 settings.json 的老安装
原地升级时也自动保持原目录。目录里会出现：`yogod.ico`（托盘图标）、
`yogod.log`（运行日志，排查「键盘不动」用）、`console.addr`（正在运行的
控制台地址）、`settings.json`。

## 命令行

```bash
yogod probe                 # 列出 HID 接口并握手一次
yogod status                # 连接、电量、配置回读
yogod theme                 # 列出 24 套主题
yogod theme pikachu         # 推一套过去（单次执行，不常驻）
yogod run                   # 前台跑引擎（Ctrl+C 退出）
yogod bench                 # 推帧压测（开发用）
yogod serve                 # 日常使用，双击等效
```

双击启动不弹黑窗（`-H windowsgui`）；从终端敲子命令，输出会接回父终端。

## Coding 伴侣

点阵屏用形状报状态，键盘背光保持当前主题不动。内置三家各有一张脸，
其余工具共用一颗宝石。六种状态灯语对任何形状自动套用：

| 状态 | 颜色 | 动效 |
|---|---|---|
| 执行中 | 橙 | 一道流光扫过形状，各家扫法不同 |
| 等你授权 | 黄 | 双跳心跳，一拍强一拍弱 |
| 等你补充 | 红 | 三连轻叩，一波弱过一波 |
| 完成 | 绿 | 亮一下，光晕从中心荡开，6 秒回空闲 |
| 出错 | 红 | 警示双闪：急两声、长一息，6 秒回空闲 |
| 空闲 | 白 | 慢呼吸，偶尔眨眼，吸气顶到纯白 |

15 分钟没动静的会话自动退回空闲。

**接入方式**：打开控制台「Coding 伴侣」页，把那段接入指令原样发给你的 AI 工具
（推荐），或手动运行：

```bash
yogod hook events                     # 打印认得的事件名
yogod hook --agent cursor --dry-run   # 拿一段 hook JSON 试翻译
yogod hook install --agent cursor     # 打印要合并进工具配置的片段
```

适配器**通用、不绑厂商**：只认事件名（`SessionStart` / `session_start` /
`session-start` 是同一个事件），`--agent` 只是给会话命名用的自由字符串，
代码里没有任何"如果是某家工具"的分支。不在表里的工具照样能接 ——
协议是开放的 `POST /api/events`。

三条硬规则（有测试盯着）：

1. **绝不阻塞工具** —— 超时 350ms、失败重试一次、任何错误吞掉、永远 exit 0
2. **只取白名单元数据** —— prompt / tool_input / 密钥 / 对话正文一律不碰
3. **只连本机** —— hook 命令的外网地址会被拒绝

## 控制台

`yogod serve` 后浏览器打开 http://127.0.0.1:25264 。页面是 embed 进二进制的单文件，
**不参与出帧** —— 关掉页面键盘照常动；预览是 SSE 推送的真实设备帧（满亮度、无校准，
即主题原始颜色），固件灯效模式下键盘预览会压灰并提示"预览暂停"。

「键盘」页的两个关键选择：

- **背光来源**：主题配色（逐键推流，和点阵成套）或原厂灯效（固件驱动，零推帧）。
- **动态 / 静态**：静态 = 点阵和背光都定格，最省电。

「系统」页：推帧档位（默认 6/10、流畅 10/20、极速 15/25 fps）、输入保护、
熄屏策略（渐暗 → 全灭）、灯珠校准（白平衡/饱和/限流，滑杆实时生效）。

## HTTP 接口

```
GET  /api/ping             健康检查
GET  /api/state            全量状态（场景/主题/电量/番茄钟/会话/丢帧统计）
GET  /api/themes           24 套主题（含 6×6 字符画）
GET  /api/patterns         48 张图案，按 6 组
GET  /api/agents           伴侣内置工具形状
POST /api/scene            {id}                  切场景（theme/pomodoro/companion/focus）
POST /api/theme            {id, dynamic}         切主题
POST /api/pattern          {id}                  图案单选（空串还原）
POST /api/brightness       {dot, keys}           亮度（只压设备，不压预览）
POST /api/keyboard         {id}                  原厂灯效（-1 = 主题配色）
POST /api/pomodoro         {action, preset?}     start/pause/skip/reset
POST /api/events           伴侣标准事件（GET = SSE 帧与状态流）
GET  /api/sessions         会话列表
POST /api/sessions         清会话
GET/POST /api/settings     配置（校准/熄屏/推帧档位/输入保护/空闲形状）
```

`/kb/*` 保留了一套兼容层（`status` / `power` / `readcfg` / `config` / `dotmode` /
`matrix` / `keys` / `stopkeys` / `reconnect`），给老脚本用。`/kb/lightbuf` 和
`/kb/anim` 返回 `ok:false` —— VIA 灯效缓冲区和"页面塞帧序列"没有移植，动画由引擎驱动。

## 键盘不动时先看这两个数

`/api/state` 的 `device` 里有 `sent` 和 `dropBy`：

```json
"device": { "sent": 1284, "dropped": 5201,
            "dropBy": { "deduped": 3120, "throttled": 1900, "typing": 181 } }
```

- `sent` 长时间不涨 → 一帧都没写进去（设备睡了 / 模式被改回去了）
- `deduped` 占大头 → 画面本来就没变，正常
- `throttled` 占大头 → 触到限速，正常
- `typing` 占大头 → 输入保护把帧攒着呢，停手 250ms 补发

还不行就看数据目录的 `yogod.log`。

## 构建

```bash
go build -trimpath -ldflags="-s -w -H windowsgui" -o yogod.exe ./cmd/yogod
```

发版用 `build.cmd`：构建 exe + 收文档 + 打成 `dist\yogod.zip`（免安装，解开即用，
沿用原项目的发布约定；约 3 MB）。

**零第三方依赖**，只用标准库 + `syscall`/`unsafe` 直调 setupapi / hid / user32 /
shell32。不需要 cgo、hidapi、WebView2。编译产物约 7 MB 单文件（其中约 4 MB 是
`net/http` 带进来的 TLS 栈 —— 本地服务用不到，但 Go 无法只编译 HTTP/1，可接受）。

## 安全护栏

- `reset_device`（cmd 49）在原版协议表里夹在常用号段中间，原项目只在注释里写"千万别发"。
  本版把它从白名单里物理移除，`proto.Packet.Marshal` 直接拒绝（有测试断言发不出去）。
- 没有 Claude 额度 / 凭据读取：原项目的 `quota` / `.credentials.json` 整个不移植。
- 伴侣适配器只取白名单元数据，喂一个带 prompt、密钥、正文的 payload 进去，
  断言发出去的 JSON 里一个敏感字节都找不到。

## 测试

```bash
go vet ./... && go test ./...
```

几个值得单独说的：

- **`internal/color` 与 `internal/theme` 是对拍的，不是手算的。**
  `testdata/gen_color_fixtures.js` 和 `gen_theme_fixtures.js` 会**真的执行原项目
  `yogo.html` 里的原始 JS**，生成期望值；Go 侧搬错一个字符、一个色值、
  一个动画参数就会红。
- **`internal/engine` 的核心断言**：写设备慢 100ms 时帧泵仍按节拍推进 ——
  对应原项目"跑久了就卡死"的老 bug。
- **`internal/api` 盯"沉默降级"**：`TestConsoleFieldContract` 把页面读的字段和
  服务端真发的 JSON 绑在一起，改任一边都会红。
- **`internal/hid` / `internal/device` 有真机集成测试**：插着键盘就真跑，没接自动 skip。
- 伴侣有状态机测试（心跳/叩门/绽放的节奏曲线）和"不许泄漏正文"测试。

## 两条关键设计

### 1. 时钟与 IO 解耦

```go
case now := <-ticker.C:
    r := scene.Frame(clock)   // 纯函数
    select {
    case out <- r:            // 1 槽邮箱
    default:
        <-out; out <- r       // 有未发出的帧？用新的覆盖它
    }
```

画面是**状态**不是队列 —— 写设备慢的时候，补发三秒前的画面毫无意义。

### 2. 设备模式以回读为准

固件会自己退回原厂模式（休眠、Fn 键切灯效、被官方 HUB 抢），那种情况下推帧
照样"写成功"但进了关闭的通道 —— 旧版的 `streamOn===254` 页面缓存永远发现不了。
本版：进场景必重写模式、写结果带 `Result{Sent, Reason}`、liveness 连续 10 秒
无真实写入就重进场景重推画面，外加每 60 秒一次只读配置对账，以及 `watchDevice`
盯 HID 拓扑（有线/2.4G 热切换不用重启）。

## 移植时踩过的坑（留给后来人）

**1. 24 套主题的 `Theme.KB` 字符画字段全是空的。**
原版没有一套主题把键盘配色写成字符画 —— 全是 `kbDiamond(...)` / `kbFlow(...)`
这样的构图函数调用。按"字符画"去读会推出一块全黑的键盘，**不报错、不 panic**。
`KBFrames()` 在字符画为空时退回构图函数的第一帧，`TestNoThemeRendersBlank` 守住这条。

**2. 三处"看起来等价"的浮点写法会让颜色差 1–2。**

| 原 JS | 天真翻译 | 后果 |
|---|---|---|
| `(((x % 1) + 1) % 1)` | `x - math.Floor(x)` | `+1` 再 `%1` 动最低位，`2.4999…9` 与 `2.5` 分道扬镳 |
| `c * 255 / mx` | `c * (255 / mx)` | 先算倒数得 `195.4999…97`，精确算得 `195.5`，round 后差 1 |
| `(h*60 + deg + 360) % 360` | `mod(h*60+deg, 360)` | 那次 `+360` 挪一个 ulp，级联到整块键盘 |

**3. `ANIMS.fire` 不是 `f(t)`。** 每帧调 `Math.random()`、且 `fireBuf` 跨帧演化，
既随机又有状态，无法像素级对拍。算法逐行照抄，
`TestFireWarmup` 锁定"火苗一帧往上爬一格"的特征（点亮格数 6→12→18→24→30→36）。

**4. 108 字节切 5 包，最后一包只有 12 字节有效。**
Python 的 `ljust` 顺手补了零，照抄成 Go 切片直接 panic
（`slice bounds out of range [:120] with length 108`）。现在分包是纯函数，有单测。

## 目录

```
cmd/yogod/            命令行入口与装配（serve 的依赖注入都在这里）
internal/proto/       报文与配置字节布局（纯函数，零依赖）
internal/color/       灯珠调色：ledize / 增益 / 饱和度 / EQL / RGB565
internal/frame/       点阵帧(108B) 与逐键帧(168B) 的编码
internal/hid/         Windows HID：SetupAPI 枚举 + overlapped 读写
internal/device/      协议事务、限速、去重、补发、电量、原厂灯效表
internal/engine/      帧泵、Scene 接口、省电闸门、liveness 对账、预览帧
internal/theme/       调色板、构图原语、24 套主题、48 张图案
internal/store/       settings.json（单一来源、原子写、可迁移）
internal/agent/       伴侣事件的状态机
internal/companion/   hook 适配器（通用、白名单、只连本机、绝不阻塞）
internal/api/         HTTP + SSE + /kb/* 兼容层 + 嵌入式控制台
internal/sys/         空闲 / 锁屏 / 托盘 / 自启 / 单实例 / Job Object
testdata/             对拍数据生成脚本与产物
```

## 致谢

原项目 [yogodot](https://github.com/Wednesque/yogodot)（Python）—— 主题、图案、
调色管线和全部动画曲线的来源。Go 版用 fixture 对拍保证移植不失真。
