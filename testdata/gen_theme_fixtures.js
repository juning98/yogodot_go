// 从 yogo.html 里抽出纯计算部分（调色板 / 构图原语 / 24 主题 / 布局 / 动画），
// 在 Node 里真跑一遍，把每套主题在若干时刻的渲染结果导成 JSON。
//
// Go 侧的 internal/theme 拿这份数据做对拍：说明"美术资产搬对了"，
// 而不是"看起来差不多"。
//
// 用法： node gen_theme_fixtures.js <yogo.html> > theme_fixtures.json
const fs = require("fs");
const path = require("path");

const htmlPath = process.argv[2] || path.join(__dirname, "yoyo.html");
const html = fs.readFileSync(htmlPath, "utf8");
const lines = html.split("\n");

// 抽出需要的行区间（1-based 行号，来自对源码的定位）。
// 这些区间里全是纯函数与字面量，没有任何 DOM 访问。
const ranges = [
  [676, 692], // PAL / rows2rgb / blank / clamp / RING / hsv
  [727, 791], // STATICS + GALLERY
  [792, 1113], // KP / mix / eachKey / 构图原语 / kbField
  [1114, 1478], // THEMES（含 FRUITS/ANIMALS 等）
  [1479, 1726], // FW_DOT / FW_KB / LAYOUTS / Object.assign / ANIMS
];

const chunks = ranges.map(([a, b]) => lines.slice(a - 1, b).join("\n"));
const source = chunks.join("\n");

// KB_MAP / KB_N 在 533-541
const kbMap = lines.slice(532, 541).join("\n");

const body = `
${kbMap}
${source}
return { THEMES, LAYOUTS, ANIMS, STATICS, GALLERY, KB_MAP, KB_N, PAL, KP };
`;

let api;
try {
  api = new Function(body)();
} catch (e) {
  process.stderr.write("抽取失败：" + e.message + "\n");
  process.exit(1);
}

// 时刻取 0 和几个非整数点，能同时覆盖"静态"和"动画相位"
const TIMES = [0, 0.37, 1.5];

function ledArray(rows) {
  // rows 可能是 eachKey 的结果（84 长，按 LED 索引），也可能返回 6×16
  if (Array.isArray(rows) && rows.length === 84) {
    return rows.map((c) => c.map((v) => clampByte(v)));
  }
  return null;
}

function clampByte(v) {
  return Math.max(0, Math.min(255, Math.round(v)));
}

const out = {
  themes: {},
  statics: {},
};

for (const [id, t] of Object.entries(api.THEMES)) {
  const entry = {
    name: t.name,
    light: t.light,
    dot: t.dot,
    // 键盘：静态一帧 + 动画若干时刻
    kb: null,
    kbAt: {},
    dotAt: {},
    hasKbAnim: typeof t.kbAnim === "function",
    hasDotAnim: typeof t.dotAnim === "function",
  };

  try {
    if (typeof t.kb === "function") {
      const kb = t.kb(0);
      if (kb) entry.kb = ledArray(kb);
    } else if (Array.isArray(t.kb)) {
      // eva01 / eva00 / minecraft 的 kb 是字符画数组，用 kbArt 转成 84 灯
      entry.kb = ledArray(kbArt(t.kb, KP));
    }
  } catch (e) {
    entry.kbError = String(e.message);
  }

  if (entry.hasKbAnim) {
    for (const tt of TIMES) {
      try {
        entry.kbAt[tt] = ledArray(t.kbAnim(tt));
      } catch (e) {
        entry.kbAt[tt] = "ERR:" + e.message;
      }
    }
  }

  if (entry.hasDotAnim) {
    for (const tt of TIMES) {
      try {
        entry.dotAt[tt] = t.dotAnim(tt).map((r) => r.map((c) => clampByte3(c)));
      } catch (e) {
        entry.dotAt[tt] = "ERR:" + e.message;
      }
    }
  }

  out.themes[id] = entry;
}

function clampByte3(c) {
  const cc = c.map(clampByte);
  while (cc.length < 3) cc.push(0);
  return cc.slice(0, 3);
}

for (const [id, art] of Object.entries(api.STATICS)) {
  out.statics[id] = art;
}

out.themeOrder = Object.keys(api.THEMES);
out.gallery = api.GALLERY;

process.stdout.write(JSON.stringify(out));
