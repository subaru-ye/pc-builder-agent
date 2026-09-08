const fs = require('fs');
const path = require('path');
const { ROOT, DATA, OUTPUT } = require('./paths.cjs');
const { spawnSync } = require('child_process');

const SKILL_DIR = process.env.MAISHOU_SKILL_DIR;
if (!SKILL_DIR) throw new Error('Set MAISHOU_SKILL_DIR to the installed taobao skill directory.');
const OUT = path.join(DATA, 'maishou_full_results_20260908.json');

// sku -> [primary, fallback...] 换词重试直到有结果
const TRY = {
  'cooler-arctic-lf2-240': ['arctic liquid freezer II 240', 'arctic LF2 240'],
  'cooler-arctic-lf3-240': ['arctic liquid freezer III 240', 'arctic LF3 240'],
  'cooler-arctic-lf3-360': ['arctic liquid freezer III 360', 'arctic LF3 360'],
  'cooler-bequiet-dark-rock-pro-4': ['be quiet Dark Rock Pro 4', '德商必酷 Dark Rock Pro 4'],
  'cooler-bequiet-dark-rock-pro-5': ['be quiet Dark Rock Pro 5', '德商必酷 Dark Rock Pro 5'],
  'cooler-coolermaster-hyper212-black': ['酷冷至尊 Hyper 212 BLACK', 'Hyper 212 Black Edition'],
  'cooler-corsair-h100i-elite-capellix-xt': ['海盗船 H100i ELITE CAPELLIX XT', '海盗船 H100i 水冷'],
  'cooler-corsair-h150i-elite-capellix-xt': ['海盗船 H150i ELITE CAPELLIX XT', '海盗船 H150i 水冷'],
  'cooler-deepcool-ag400': ['九州风神 AG400'],
  'cooler-deepcool-ak620': ['九州风神 AK620'],
  'cooler-deepcool-assassin-iv': ['九州风神 ASSASSIN IV', '九州风神 阿萨辛4'],
  'cooler-deepcool-lt520': ['九州风神 LT520'],
  'cooler-msi-mag-coreliquid-e360': ['微星 CORELIQUID E360', '微星 寒霜 E360'],
  'cooler-noctua-nh-d15': ['猫头鹰 NH-D15', 'noctua NH-D15'],
  'cooler-noctua-nh-u12s': ['猫头鹰 NH-U12S', 'noctua NH-U12S'],
  'cooler-nzxt-kraken-240': ['NZXT Kraken 240', '恩杰 海妖 240'],
  'cooler-nzxt-kraken-x63': ['NZXT Kraken X63', '恩杰 海妖 X63'],
  'cooler-thermalright-frozen-prism-240': ['利民 Frozen Prism 240', '利民 冰封棱镜 240'],
  'cooler-thermalright-pa120se': ['利民 PA120 SE', '利民 PA120SE'],
  'cooler-thermalright-ps120se': ['利民 PS120 SE', '利民 PS120SE'],
  'gpu-gb-5060-windforce': ['技嘉 RTX 5060 WINDFORCE', '技嘉 5060 风神'],
  'gpu-gb-5060ti-windforce-16g': ['技嘉 RTX 5060 Ti WINDFORCE', '技嘉 5060Ti 风神'],
  'gpu-sapphire-9060xt-pulse-16g': ['蓝宝石 RX 9060XT 白金', '蓝宝石 9060XT'],
  'mb-asrock-b650m-hdv-m2': ['华擎 B650M HDV M.2', '华擎 B650M-HDV'],
  'mb-gb-b550-aorus-elite-v2': ['技嘉 B550 AORUS ELITE V2 主板', '技嘉 B550 小雕'],
  'mb-msi-b860-tomahawk-wifi': ['微星 B860 TOMAHAWK', '微星 B860 战斧'],
  'mem-gskill-ripjawsv-32-3200': ['芝奇 焰光戟 3200 16G×2', '芝奇 Ripjaws V DDR4 3200 16Gx2'],
  'mem-gskill-z5neo-rgb-32-6000': ['芝奇 幻锋戟Neo DDR5 6000', '芝奇 幻锋戟 NEO 6000 16G×2'],
  'mem-team-delta-32-3200-d4': ['十铨 Delta DDR4 3200', '十铨 DELTA 3200 16Gx2'],
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
const results = JSON.parse(fs.readFileSync(OUT, 'utf8'));
let done = 0;

(async () => {
  for (const [sku, kws] of Object.entries(TRY)) {
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
    console.log(`[${done}/${Object.keys(TRY).length}] ${sku} -> ${hit ? hit.length : 0} rows (kw: ${usedKw || 'FAILED'})`);
    await sleep(1200);
  }
  const total = Object.keys(results).length;
  const ok = Object.values(results).filter((v) => v.rows.length).length;
  console.log(`DONE: ${ok}/${total} skus with results -> ${OUT}`);
})();
