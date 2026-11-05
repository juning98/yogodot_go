// shot.mjs —— 控制台截图工具（docs 配图用）。
//
// 为什么不直接用 chrome --headless --screenshot：控制台是 SSE 长连接驱动的
// 活页面，--virtual-time-budget 会永远等不到流结束而卡死。这里走 CDP：
// 导航 → 真等几秒让 SSE 帧渲染上屏 → Page.captureScreenshot。
//
// 用法：
//   1. 先起无头 Chrome：chrome --headless=new --remote-debugging-port=9222 --window-size=1440,900 about:blank
//   2. node docs/design/shot.mjs <控制台基址> <输出目录> [等待毫秒]
//      例：node docs/design/shot.mjs http://127.0.0.1:8794 docs/img 6000
//
// 拍哪些页由 SHOTS 决定；页面导航是 hash 路由（console.html 的 go()）。
import { writeFile } from 'node:fs/promises';
import path from 'node:path';

const SHOTS = [
  ['theme', 'console-theme.png'],
  ['keyboard', 'console-keyboard.png'],
  ['companion', 'console-companion.png'],
  ['pomodoro', 'console-pomodoro.png'],
];

const [base = 'http://127.0.0.1:8794', outDir = 'docs/img', waitMs = '6000', only = ''] = process.argv.slice(2);
const wait = Number(waitMs);
// 第 4 参可只拍指定页（逗号分隔）：切场景/灯效后要分批拍时用。
const shots = only ? SHOTS.filter(([p]) => only.split(',').includes(p)) : SHOTS;

const ver = await (await fetch('http://127.0.0.1:9222/json/version')).json();
const ws = new WebSocket(ver.webSocketDebuggerUrl);
await new Promise((res, rej) => { ws.onopen = res; ws.onerror = rej; });

let seq = 0;
const pending = new Map();
ws.onmessage = (ev) => {
  const msg = JSON.parse(ev.data);
  if (msg.id && pending.has(msg.id)) {
    const { res, rej } = pending.get(msg.id);
    pending.delete(msg.id);
    msg.error ? rej(new Error(msg.error.message)) : res(msg.result);
  }
};
function cdp(method, params = {}, sessionId) {
  const id = ++seq;
  ws.send(JSON.stringify({ id, method, params, sessionId }));
  return new Promise((res, rej) => pending.set(id, { res, rej }));
}

for (const [page, file] of shots) {
  const { targetId } = await cdp('Target.createTarget', { url: `${base}/#${page}` });
  const { sessionId } = await cdp('Target.attachToTarget', { targetId, flatten: true });
  await cdp('Page.enable', {}, sessionId);
  await new Promise((r) => setTimeout(r, wait)); // 真等：SSE 连上、首帧画出来
  const { data } = await cdp('Page.captureScreenshot', { format: 'png' }, sessionId);
  await writeFile(path.join(outDir, file), Buffer.from(data, 'base64'));
  await cdp('Target.closeTarget', { targetId });
  console.log('✓', file);
}
ws.close();
