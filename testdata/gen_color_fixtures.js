// 从 yogo.html 里原样抽出色彩管线（GAIN/SAT/EQL/CORR + ledize + gr），
// 用真实的 JS 跑一遍，输出 Go 测试用的对拍数据。
//
// 这样 Go 侧的实现就有了 ground truth —— 不是"我觉得算对了"，
// 而是"和原始实现逐位相同"。
const fs = require("fs");
const path = require("path");

const htmlPath =
  process.argv[2] || path.join(__dirname, "..", "..", "yogodot", "yogo.html");
const html = fs.readFileSync(htmlPath, "utf8");
const lines = html.split("\n");

function slice(startMarker, endMarker, fromLine = 0) {
  const start = lines.findIndex(
    (l, i) => i >= fromLine && l.includes(startMarker),
  );
  if (start < 0) throw new Error("找不到起点: " + startMarker);
  const end = lines.findIndex((l, i) => i > start && l.includes(endMarker));
  if (end < 0) throw new Error("找不到终点: " + endMarker);
  return lines.slice(start, end + 1).join("\n");
}

// clamp 在 yogo.html:685
const clampSrc = "const clamp=v=>Math.max(0,Math.min(255,Math.round(v)));";
// GAIN(547) .. ledize 结束(606)
const pipeline = slice("let GAIN=", "return [mx-(mx-c[0])*ratio", 540);

const sandbox = {};
const code = `
${clampSrc}
${pipeline}
// 补齐 ledize 的收尾（slice 停在 return 之前）
`;
// slice 可能在函数中间截断，改用手工补一个大括号平衡
let src = code;
const open = (src.match(/{/g) || []).length;
const close = (src.match(/}/g) || []).length;
for (let i = 0; i < open - close; i++) src += "\n}";

const factory = new Function(
  "localStorage",
  src + "\nreturn {gr, ledize, GAIN, SAT, EQL, CORR};",
);

const stub = { getItem: () => null, setItem: () => {}, removeItem: () => {} };
const api = factory(stub);

// 对拍样本：覆盖纯色、灰色、低饱和、极端通道组合
const samples = [
  [0, 0, 0],
  [255, 255, 255],
  [255, 0, 0],
  [0, 255, 0],
  [0, 0, 255],
  [255, 40, 35],
  [255, 195, 0],
  [217, 119, 87],
  [0, 185, 235],
  [140, 65, 240],
  [132, 132, 148],
  [20, 12, 4],
  [6, 8, 14],
  [118, 52, 32],
  [245, 165, 120],
  [110, 235, 120],
  [108, 162, 212],
  [250, 110, 72],
  [62, 62, 74],
  [165, 72, 0],
];

const clamp = (v) => Math.max(0, Math.min(255, Math.round(v)));

const rows = samples.map((c) => {
  const ids = api.ledize(c);
  const g = api.gr(ids);
  return {
    in: c,
    ledize: ids.map(clamp),
    gr: g.map(clamp),
  };
});

const out = {
  params: { GAIN: api.GAIN, SAT: api.SAT, EQL: api.EQL, CORR: api.CORR },
  rows,
  // 亮度也要对拍：keys 走 gr，dot 默认不校正、只乘亮度
  brightness: [0, 25, 50, 70, 90, 100].map((k) => ({
    k,
    dot: [255, 195, 0].map((v) => clamp((v * k) / 100)),
    keys: rows[7].gr.map((v) => clamp((v * k) / 100)),
  })),
};

process.stdout.write(JSON.stringify(out, null, 2));
