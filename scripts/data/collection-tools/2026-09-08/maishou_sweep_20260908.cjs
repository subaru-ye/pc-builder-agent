const fs = require('fs');
const path = require('path');
const { ROOT, DATA, OUTPUT } = require('./paths.cjs');
const { spawnSync } = require('child_process');

const SKILL_DIR = process.env.MAISHOU_SKILL_DIR;
if (!SKILL_DIR) throw new Error('Set MAISHOU_SKILL_DIR to the installed taobao skill directory.');
const OUT = path.join(DATA, 'maishou_full_results_20260908.json');

// sku -> 搜索词（品牌中文化；内存容量粒度防套条误绑；case/cooler 加品类词提高召回）
const KW = {
  'cpu-i3-12100f':'i3 12100F','cpu-i5-12400f':'i5 12400F','cpu-i5-12600k':'i5 12600K',
  'cpu-i5-13400f':'i5 13400F','cpu-i5-14600kf':'i5 14600KF','cpu-i7-14700k':'i7 14700K',
  'cpu-r5-5500':'锐龙5 5500','cpu-r5-5600':'锐龙5 5600','cpu-r5-7500f':'锐龙5 7500F',
  'cpu-r5-7600':'锐龙5 7600','cpu-r5-7600x':'锐龙5 7600X','cpu-r5-9600x':'锐龙5 9600X',
  'cpu-r7-5700x':'锐龙7 5700X','cpu-r7-7700':'锐龙7 7700','cpu-r7-7800x3d':'锐龙7 7800X3D',
  'cpu-r7-9700x':'锐龙7 9700X','cpu-r7-9800x3d':'锐龙7 9800X3D','cpu-r9-7900':'锐龙9 7900',
  'cpu-r9-9900x':'锐龙9 9900X','cpu-ultra5-245k':'Ultra 5 245K',
  'gpu-asus-4060ti-dual-evo':'华硕 4060Ti DUAL EVO','gpu-asus-4070tis-tuf':'华硕 4070Ti SUPER TUF',
  'gpu-asus-4080s-tuf':'华硕 4080SUPER TUF','gpu-gb-4070-windforce':'技嘉 RTX4070 WINDFORCE',
  'gpu-gb-5060-windforce':'技嘉 RTX5060 WINDFORCE','gpu-gb-5060ti-windforce-16g':'技嘉 RTX5060Ti WINDFORCE 16G',
  'gpu-gb-5070-windforce-sff':'技嘉 RTX5070 WINDFORCE','gpu-gb-5080-gaming':'技嘉 RTX5080 GAMING OC',
  'gpu-intel-b580-le':'蓝戟 B580','gpu-msi-3060-ventus2x':'微星 RTX3060 VENTUS 2X',
  'gpu-msi-4060-ventus2x-black':'微星 RTX4060 VENTUS BLACK','gpu-msi-4070s-ventus2x':'微星 RTX4070SUPER VENTUS 2X',
  'gpu-msi-5070ti-ventus3x':'微星 RTX5070Ti VENTUS 3X','gpu-sapphire-6600-pulse':'蓝宝石 RX6600 白金',
  'gpu-sapphire-7600-pulse':'蓝宝石 RX7600 白金','gpu-sapphire-7700xt-pulse':'蓝宝石 RX7700XT 白金',
  'gpu-sapphire-7800xt-pulse':'蓝宝石 RX7800XT 白金','gpu-sapphire-7900xt-pulse':'蓝宝石 RX7900XT 白金',
  'gpu-sapphire-9060xt-pulse-16g':'蓝宝石 RX9060XT 白金 16G','gpu-sapphire-9070xt-pulse':'蓝宝石 RX9070XT 白金',
  'mb-asrock-b650m-hdv-m2':'华擎 B650M-HDV/M.2','mb-asus-b650e-f-strix':'华硕 ROG STRIX B650E-F',
  'mb-gb-b550-aorus-elite-v2':'技嘉 B550 AORUS ELITE V2','mb-gb-b650-aorus-elite-ax':'技嘉 B650 AORUS ELITE AX',
  'mb-gb-b650i-aorus-ultra':'技嘉 B650I AORUS ULTRA','mb-gb-b760-gaming-x-ax':'技嘉 B760 GAMING X AX',
  'mb-gb-x870-eagle-wifi7':'技嘉 X870 EAGLE WIFI7','mb-msi-b550-tomahawk':'微星 MAG B550 TOMAHAWK',
  'mb-msi-b550m-pro-vdh-wifi':'微星 B550M PRO-VDH WIFI','mb-msi-b650-tomahawk-wifi':'微星 MAG B650 TOMAHAWK WIFI',
  'mb-msi-b650m-mortar-wifi':'微星 MAG B650M MORTAR WIFI','mb-msi-b760m-a-wifi':'微星 PRO B760M-A WIFI',
  'mb-msi-b760m-a-wifi-ddr4':'微星 PRO B760M-A WIFI DDR4','mb-msi-b850-tomahawk-max':'微星 MAG B850 TOMAHAWK MAX',
  'mb-msi-b860-tomahawk-wifi':'微星 MAG B860 TOMAHAWK WIFI','mb-msi-b860m-a-wifi':'微星 PRO B860M-A WIFI',
  'mb-msi-h610m-g-ddr4':'微星 PRO H610M-G DDR4','mb-msi-pro-a620m-e':'微星 PRO A620M-E',
  'mb-msi-z790-p-wifi':'微星 PRO Z790-P WIFI','mb-msi-z890-tomahawk-wifi':'微星 MAG Z890 TOMAHAWK WIFI',
  'mem-adata-lancer-32-5200':'威刚 LANCER DDR5 5200 16G×2',
  'mem-corsair-lpx-16-3200':'海盗船 LPX DDR4 3200 8G×2','mem-corsair-lpx-32-3600':'海盗船 LPX DDR4 3600 16G×2',
  'mem-corsair-veng-32-6000':'海盗船 复仇者 DDR5 6000 16G×2','mem-corsair-veng-rgb-32-6000':'海盗船 复仇者 RGB DDR5 6000 16G×2',
  'mem-crucial-ballistix-16-3200':'英睿达 铂胜 DDR4 3200 8G×2',
  'mem-gskill-flarex5-32-6000':'芝奇 焰锋戟 DDR5 6000 16G×2','mem-gskill-ripjawsv-16-3600':'芝奇 焰光戟 DDR4 3600 8G×2',
  'mem-gskill-ripjawsv-32-3200':'芝奇 焰光戟 DDR4 3200 16G×2','mem-gskill-s5-32-6000':'芝奇 幻锋戟 DDR5 6000 16G×2',
  'mem-gskill-z5-rgb-32-6400':'芝奇 幻锋戟 RGB DDR5 6400 16G×2','mem-gskill-z5neo-rgb-32-6000':'芝奇 幻锋戟 Neo DDR5 6000 16G×2',
  'mem-kingston-beast-16-3200-d4':'金士顿 野兽 DDR4 3200 8G×2','mem-kingston-beast-16-5200':'金士顿 野兽 DDR5 5200 8G×2',
  'mem-kingston-beast-16-6000':'金士顿 野兽 DDR5 6000 8G×2','mem-kingston-beast-32-3600-d4':'金士顿 野兽 DDR4 3600 16G×2',
  'mem-kingston-beast-32-5600':'金士顿 野兽 DDR5 5600 16G×2','mem-kingston-beast-32-6000':'金士顿 野兽 DDR5 6000 16G×2',
  'mem-team-delta-32-3200-d4':'十铨 Delta DDR4 3200 16G×2','mem-team-delta-32-6000-white':'十铨 Delta DDR5 6000 白色 16G×2',
  'ssd-adata-legend800-1tb':'威刚 LEGEND 800 1TB','ssd-adata-s70blade-1tb':'威刚 S70 Blade 1TB',
  'ssd-crucial-bx500-1tb':'英睿达 BX500 1TB','ssd-crucial-mx500-1tb':'英睿达 MX500 1TB',
  'ssd-crucial-p3plus-1tb':'英睿达 P3Plus 1TB','ssd-crucial-p5plus-1tb':'英睿达 P5 Plus 1TB',
  'ssd-crucial-t500-2tb':'英睿达 T500 2TB','ssd-intel-670p-1tb':'英特尔 670P 1TB',
  'ssd-kingston-a400-480gb':'金士顿 A400 480G','ssd-kingston-kc3000-1tb':'金士顿 KC3000 1TB',
  'ssd-kingston-nv2-1tb':'金士顿 NV2 1TB','ssd-samsung-870evo-1tb':'三星 870 EVO 1TB',
  'ssd-samsung-870qvo-2tb':'三星 870 QVO 2TB','ssd-samsung-970evoplus-1tb':'三星 970 EVO Plus 1TB',
  'ssd-samsung-980pro-1tb':'三星 980 PRO 1TB','ssd-samsung-990pro-2tb':'三星 990 PRO 2TB',
  'ssd-wd-sa510-1tb':'西数 SA510 1TB','ssd-wd-sn580-1tb':'西数 SN580 1TB',
  'ssd-wd-sn770-1tb':'西数 SN770 1TB','ssd-wd-sn850x-2tb':'西数 SN850X 2TB',
  'psu-bq-pp12m-750':'be quiet Pure Power 12 M 750W','psu-bq-pp12m-850':'be quiet Pure Power 12 M 850W',
  'psu-bq-sp12-1000':'be quiet Straight Power 12 1000W','psu-bq-sp12-750':'be quiet Straight Power 12 750W',
  'psu-corsair-cx650m':'海盗船 CX650M','psu-corsair-hx1000i-2022':'海盗船 HX1000i',
  'psu-corsair-rm1000x-2021':'海盗船 RM1000x','psu-corsair-rm750e':'海盗船 RM750e',
  'psu-corsair-rm850e':'海盗船 RM850e','psu-corsair-rm850x-2021':'海盗船 RM850x',
  'psu-gb-ud850gm-pg5':'技嘉 UD850GM PG5','psu-msi-mag-a650bn':'微星 MAG A650BN',
  'psu-msi-mag-a750gl':'微星 MAG A750GL','psu-msi-mag-a850gl':'微星 MAG A850GL',
  'psu-msi-mpg-a850g':'微星 MPG A850G','psu-seasonic-focus-gx750-atx30':'海韵 FOCUS GX-750',
  'psu-seasonic-focus-gx850-atx30':'海韵 FOCUS GX-850','psu-seasonic-vertex-gx1000':'海韵 VERTEX GX-1000',
  'psu-tt-gf3-750':'Tt GF3 750W 金牌','psu-tt-gf3-850':'Tt GF3 850W 金牌',
  'case-asus-prime-ap201':'华硕 AP201 机箱','case-bequiet-pure-base-500dx':'be quiet Pure Base 500DX',
  'case-bequiet-shadow-base-800-fx':'be quiet Shadow Base 800 FX','case-coolermaster-nr200p':'酷冷至尊 NR200P',
  'case-coolermaster-td500-mesh-v2':'酷冷至尊 TD500 MESH','case-corsair-4000d-airflow':'海盗船 4000D AIRFLOW',
  'case-corsair-5000d-airflow':'海盗船 5000D AIRFLOW','case-fractal-define-7':'分形工艺 Define 7 机箱',
  'case-fractal-meshify-2-compact':'分形工艺 Meshify 2 Compact','case-fractal-north':'分形工艺 North 机箱',
  'case-fractal-pop-air':'Fractal Pop Air 机箱','case-fractal-terra':'分形工艺 Terra 机箱',
  'case-fractal-torrent':'分形工艺 Torrent 机箱','case-lianli-a3-matx':'联力 A3-mATX 机箱',
  'case-lianli-lancool-216':'联力 LANCOOL 216','case-lianli-o11-dynamic-evo':'联力 O11 Dynamic EVO',
  'case-montech-air-903-max':'Montech AIR 903 MAX','case-nzxt-h5-flow-2024':'NZXT H5 Flow 机箱',
  'case-nzxt-h6-flow-2023':'NZXT H6 Flow 机箱','case-nzxt-h7-flow-2024':'NZXT H7 Flow 机箱',
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
const skus = Object.keys(KW);
const results = {};
let done = 0;

(async () => {
  fs.writeFileSync(OUT, '{}\n');
  for (const sku of skus) {
    const kw = KW[sku];
    let r = runSearch(kw);
    if (!r.rows.length) {
      await sleep(1500);
      r = runSearch(kw);
    }
    results[sku] = { keyword: kw, rows: r.rows, error: r.rows.length ? null : (r.err || 'no_results') };
    done++;
    fs.writeFileSync(OUT, JSON.stringify(results, null, 1));
    console.log(`[${done}/${skus.length}] ${sku} -> ${r.rows.length} rows`);
    await sleep(1200);
  }
  const ok = Object.values(results).filter((v) => v.rows.length).length;
  console.log(`DONE: ${ok}/${skus.length} keywords with results -> ${OUT}`);
})();
