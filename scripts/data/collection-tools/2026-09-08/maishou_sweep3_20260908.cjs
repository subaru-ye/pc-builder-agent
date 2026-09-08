const fs = require('fs');
const path = require('path');
const { ROOT, DATA, OUTPUT } = require('./paths.cjs');
const { spawnSync } = require('child_process');

const SKILL_DIR = process.env.MAISHOU_SKILL_DIR;
if (!SKILL_DIR) throw new Error('Set MAISHOU_SKILL_DIR to the installed taobao skill directory.');
const OUT = path.join(DATA, 'maishou_sweep3_results_20260908.json');

// 78 个沿用 SKU 的换词重扫：中文品牌名/序列中文名/消歧规格词
const TRY = {
  // CPU（4）：加 盒装/原盒 排散片
  'cpu-i3-12100f': ['Intel 酷睿 i3-12100F 盒装', 'i3 12100F 原盒'],
  'cpu-i5-12600k': ['Intel 酷睿 i5-12600K 盒装', 'i5 12600K 原盒'],
  'cpu-r9-7900': ['AMD 锐龙9 7900 盒装', 'R9 7900 原盒'],
  'cpu-ultra5-245k': ['酷睿 Ultra 5 245K 盒装', 'Ultra 5 245K 处理器'],
  // GPU（16）
  'gpu-asus-4060ti-dual-evo': ['华硕 RTX4060Ti DUAL EVO', '华硕 4060Ti 大雪 EVO'],
  'gpu-gb-5060-windforce': ['技嘉RTX5060风之力', '技嘉 RTX5060 风之力 OC 8G'],
  'gpu-gb-5060ti-windforce-16g': ['技嘉RTX5060TI风之力16G', '技嘉 RTX5060Ti 风之力 OC 16G'],
  'gpu-gb-5070-windforce-sff': ['技嘉 RTX5070 风之力 SFF', '技嘉 5070 风之力 SFF 12G'],
  'gpu-gb-5080-gaming': ['技嘉RTX5080魔鹰', '技嘉 RTX5080 魔鹰 OC 16G'],
  'gpu-intel-b580-le': ['蓝戟 B580 12G', '英特尔 Arc B580 LE'],
  'gpu-msi-3060-ventus2x': ['微星 RTX3060 万图师 12G', '微星 RTX3060 万图师 2X'],
  'gpu-msi-4060-ventus2x-black': ['微星 RTX4060 万图师 黑色', '微星 RTX4060 万图师 8G'],
  'gpu-msi-4070s-ventus2x': ['微星 RTX4070S 万图师 12G', '微星 RTX4070 SUPER 万图师'],
  'gpu-msi-5070ti-ventus3x': ['微星 RTX5070Ti 万图师 16G', '微星 RTX5070Ti 万图师 3X'],
  'gpu-sapphire-6600-pulse': ['蓝宝石 RX6600 白金版', '蓝宝石 RX6600 8G 白金'],
  'gpu-sapphire-7600-pulse': ['蓝宝石 RX7600 白金版', '蓝宝石 RX7600 8G 白金'],
  'gpu-sapphire-7700xt-pulse': ['蓝宝石 RX7700XT 白金版', '蓝宝石 RX7700XT 12G 白金'],
  'gpu-sapphire-7900xt-pulse': ['蓝宝石 RX7900XT 白金版', '蓝宝石 RX7900XT 20G 白金'],
  'gpu-sapphire-9060xt-pulse-16g': ['蓝宝石 RX9060XT 白金版 16G', '蓝宝石 RX9060XT 16G 白金'],
  'gpu-sapphire-9070xt-pulse': ['蓝宝石 RX9070XT 白金版', '蓝宝石 RX9070XT 16G 白金'],
  // MB（8）
  'mb-gb-b550-aorus-elite-v2': ['技嘉 B550 小雕 V2 主板', '技嘉B550AORUSELITEV2'],
  'mb-gb-b650-aorus-elite-ax': ['技嘉 B650 小雕 AX 主板', '技嘉B650小雕AX'],
  'mb-gb-b650i-aorus-ultra': ['技嘉 B650I 小雕 ITX', '技嘉 B650I AORUS ULTRA 主板'],
  'mb-msi-b550-tomahawk': ['微星 B550 战斧导弹 主板', '微星MAGB550战斧导弹'],
  'mb-gb-b760-gaming-x-ax': ['技嘉 B760 GAMING X AX 主板', '技嘉 B760 魔鹰X AX'],
  'mb-msi-b860-tomahawk-wifi': ['微星 B860 战斧导弹 ATX', '微星 B860 战斧导弹 WIFI 主板'],
  'mb-asrock-b650m-hdv-m2': ['华擎 B650M-HDV/M.2 主板', '华擎 B650M HDV M.2 全新'],
  'mb-msi-b650-tomahawk-wifi': ['微星 B650 战斧导弹 WIFI 主板', '微星MAGB650战斧导弹WIFI'],
  // MEM（10）
  'mem-corsair-lpx-32-3600': ['海盗船 复仇者 LPX DDR4 3600 16G*2', '海盗船 LPX 3600 32G 套装'],
  'mem-corsair-veng-32-6000': ['海盗船 复仇者 DDR5 6000 C30 16G*2', '海盗船 复仇者 6000 32G 套装'],
  'mem-corsair-veng-rgb-32-6000': ['海盗船 复仇者 RGB DDR5 6000 16G*2', '海盗船 复仇者RGB 6000 套条'],
  'mem-gskill-s5-32-6000': ['芝奇 幻飓戟 DDR5 6000 16G*2', '芝奇 焰刃 6000 16G*2'],
  'mem-gskill-z5-rgb-32-6400': ['芝奇 幻锋戟 RGB 6400 16G*2', '芝奇 幻锋戟RGB 6400 32G'],
  'mem-gskill-z5neo-rgb-32-6000': ['芝奇 幻锋戟 Neo EXPO 6000 16G*2', '芝奇 NEO 6000 32G 套装'],
  'mem-adata-lancer-32-5200': ['威刚 威龙 DDR5 5200 16G*2', 'XPG 威龙 DDR5 5200'],
  'mem-team-delta-32-3200-d4': ['十铨 DELTA DDR4 3200 全新 16G*2', '十铨 三角洲 DDR4 3200'],
  'mem-team-delta-32-6000-white': ['十铨 DELTA DDR5 6000 白色 16G*2', '十铨 DELTA RGB DDR5 6000 白'],
  'mem-gskill-ripjawsv-32-3200': ['芝奇 焰光戟 DDR4 3200 16G*2 全新', '芝奇 Ripjaws V 3200 16Gx2'],
  // SSD（11）
  'ssd-crucial-p3plus-1tb': ['英睿达 P3 Plus 1TB 全新', '英睿达 P3PLUS 1TB NVMe'],
  'ssd-crucial-p5plus-1tb': ['英睿达 P5 Plus 1TB 全新', '英睿达 P5PLUS 1TB NVMe'],
  'ssd-intel-670p-1tb': ['英特尔 670P 1TB 固态硬盘', 'Intel 670p 1TB 全新'],
  'ssd-samsung-870evo-1tb': ['三星 870EVO 1TB', '三星 870 EVO 1TB 全新'],
  'ssd-samsung-870qvo-2tb': ['三星 870QVO 2TB 全新', '三星 870 QVO 2TB 固态'],
  'ssd-samsung-970evoplus-1tb': ['三星 970EVO Plus 1TB', '三星 970 EVO PLUS 1TB 全新'],
  'ssd-samsung-980pro-1tb': ['三星 980PRO 1TB 固态硬盘', '三星 980 PRO 1TB 全新'],
  'ssd-wd-sn580-1tb': ['西数 SN580 1TB', 'WD 蓝盘 SN580 1TB'],
  'ssd-wd-sn770-1tb': ['西数 黑盘 SN770 1TB', 'WD SN770 1TB 全新'],
  'ssd-adata-legend800-1tb': ['威刚 LEGEND 800 1TB', 'ADATA 传奇 800 1TB'],
  'ssd-adata-s70blade-1tb': ['威刚 翼龙 S70 Blade 1TB', 'XPG S70 BLADE 1TB'],
  // PSU（6）
  'psu-bq-pp12m-750': ['德商德静界 Pure Power 12M 750W', '德商德静界 750W 金牌 电源'],
  'psu-bq-sp12-750': ['德商德静界 Straight Power 12 750W', '德商德静界 750W 白金 电源'],
  'psu-bq-sp12-1000': ['德商德静界 Straight Power 12 1000W', '德商德静界 1000W 白金 电源'],
  'psu-corsair-cx650m': ['海盗船 CX650M 650W 电源', '美商海盗船 CX650M 铜牌'],
  'psu-msi-mpg-a850g': ['微星 A850G 850W 电源', '微星 MPG A850G PCIE5'],
  'psu-seasonic-focus-gx750-atx30': ['海韵 Focus GX750 全模组', '海韵 GX750 750W 电源'],
  // CASE（13）
  'case-bequiet-pure-base-500dx': ['德商德静界 Pure Base 500DX 机箱', '德商德静界 500DX'],
  'case-bequiet-shadow-base-800-fx': ['德商德静界 Shadow Base 800 机箱', '德商德静界 800 FX'],
  'case-corsair-4000d-airflow': ['海盗船 4000D AIRFLOW 黑色', '海盗船 4000D 风道 机箱'],
  'case-corsair-5000d-airflow': ['海盗船 5000D AIRFLOW 黑色', '海盗船 5000D 风道 机箱'],
  'case-fractal-define-7': ['分形工艺 Define 7 静音 黑色', 'Fractal Define 7 标准'],
  'case-fractal-meshify-2-compact': ['分形工艺 Meshify 2 Compact 机箱', '分形工艺 Meshify2C'],
  'case-fractal-pop-air': ['分形工艺 Pop Air 黑色 机箱', '分形工艺 POP AIR 中塔'],
  'case-lianli-lancool-216': ['联力 鬼斧216 机箱', '联力 LANCOOL 216 黑色'],
  'case-lianli-o11-dynamic-evo': ['联力 包豪斯O11D EVO 机箱', '联力 O11 EVO 黑色'],
  'case-montech-air-903-max': ['Montech AIR 903 MAX 机箱', 'montech 903 MAX 黑色'],
  'case-nzxt-h5-flow-2024': ['恩杰 H5 Flow 机箱', 'NZXT H5 Flow 黑色'],
  'case-nzxt-h6-flow-2023': ['恩杰 H6 Flow 机箱', 'NZXT H6 Flow 黑色'],
  'case-nzxt-h7-flow-2024': ['恩杰 H7 Flow 机箱', 'NZXT H7 Flow 黑色'],
  // COOLER（10）
  'cooler-arctic-lf2-240': ['arctic LF II 240 水冷', 'arctic 液冷 240 二代'],
  'cooler-bequiet-dark-rock-pro-4': ['德商德静界 Dark Rock Pro 4', '德商必酷 Dark Rock Pro 4'],
  'cooler-corsair-h100i-elite-capellix-xt': ['海盗船 H100i ELITE 水冷 240', '海盗船 H100i CAPELLIX'],
  'cooler-corsair-h150i-elite-capellix-xt': ['海盗船 H150i ELITE 水冷 360', '海盗船 H150i CAPELLIX'],
  'cooler-deepcool-ag400': ['九州风神 AG400 单塔 无光', '九州风神 AG400 标准版'],
  'cooler-deepcool-lt520': ['九州风神 LT520 水冷', '九州风神 LT520 白色'],
  'cooler-msi-mag-coreliquid-e360': ['微星 E360 水冷', '微星 寒霜 360 一体式水冷'],
  'cooler-nzxt-kraken-240': ['恩杰 海妖240 一体式水冷', 'NZXT 海妖 240'],
  'cooler-nzxt-kraken-x63': ['恩杰 海妖X63 水冷', 'NZXT 海妖 X63 280'],
  'cooler-noctua-nh-d15': ['猫头鹰 NH-D15 双塔', 'noctua D15 散热器'],
};

function parseCSVLine(line) {
  const out = [];
  let cur = '', inQ = false;
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    if (inQ) {
      if (c === '"') { if (line[i + 1] === '"') { cur += '"'; i++; } else inQ = false; }
      else cur += c;
    } else if (c === '"') inQ = true;
    else if (c === ',') { out.push(cur); cur = ''; }
    else cur += c;
  }
  out.push(cur);
  return out;
}

function runSearch(keyword) {
  const r = spawnSync('uv', ['run', 'scripts/main.py', 'search', '--source=0', `--keyword=${keyword}`], {
    cwd: SKILL_DIR, encoding: 'utf8', timeout: 60000, shell: false,
  });
  const text = (r.stdout || '').trim();
  if (!text) return { rows: [], err: (r.stderr || r.status || 'empty').toString().slice(0, 200) };
  const lines = text.split(/\r?\n/).filter(Boolean);
  const header = parseCSVLine(lines[0]);
  const rows = [];
  for (const l of lines.slice(1)) {
    const cells = parseCSVLine(l);
    if (cells.length !== header.length) continue;
    const o = {};
    header.forEach((h, i) => { o[h] = cells[i]; });
    rows.push(o);
  }
  return { rows, err: null };
}

const sleep = (ms) => new Promise((res) => setTimeout(res, ms));
let results = {};
if (fs.existsSync(OUT)) results = JSON.parse(fs.readFileSync(OUT, 'utf8'));
const keys = Object.keys(TRY);
let done = 0;

(async () => {
  for (const sku of keys) {
    if (results[sku] && results[sku].rows && results[sku].rows.length) { done++; continue; }
    const kws = TRY[sku];
    let hit = null, usedKw = null, lastErr = 'no_results';
    for (const kw of kws) {
      let r = runSearch(kw);
      if (!r.rows.length) { await sleep(1500); r = runSearch(kw); }
      if (r.rows.length) { hit = r.rows; usedKw = kw; break; }
      lastErr = r.err || 'no_results';
      await sleep(1200);
    }
    results[sku] = { keyword: usedKw || kws[0], rows: hit || [], error: hit ? null : lastErr };
    done++;
    fs.writeFileSync(OUT, JSON.stringify(results, null, 1));
    console.log(`[${done}/${keys.length}] ${sku} -> ${hit ? hit.length : 0} rows (kw: ${usedKw || 'FAILED'})`);
    await sleep(1200);
  }
  const ok = keys.filter((k) => results[k] && results[k].rows.length).length;
  console.log(`DONE: ${ok}/${keys.length} skus with results -> ${OUT}`);
})();
