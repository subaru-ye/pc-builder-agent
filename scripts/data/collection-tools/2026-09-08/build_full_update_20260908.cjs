// 离线重建到 COLLECTION_OUTPUT_DIR（默认本地批次目录/rebuild），不覆盖仓库快照。
// 落盘 2026-09-08 全量数据大更新（v3.1：含 78 个沿用 SKU 的换词复扫晋升）：
// 1) OUTPUT/learning-price-rows-full-20260908.jsonl  学习行（含 accept / candidate_reject / carry_evidence）
// 2) OUTPUT/2026-09-08.csv                          基线 v3.1（102 accept + 58 carry，160 行）
// 决策依据：DATA 中 maishou_review_20260908.txt + maishou_review3_20260908.txt + maishou_probe3_20260908.txt 人工复核
const fs = require('fs');
const path = require('path');
const { ROOT, DATA, OUTPUT } = require('./paths.cjs');

const RESULTS = JSON.parse(fs.readFileSync(path.join(DATA, 'maishou_merged_20260908.json'), 'utf8'));
const CANDS = JSON.parse(fs.readFileSync(path.join(DATA, 'maishou_match_candidates3_20260908.json'), 'utf8'));

const sq = (s) => String(s || '').toLowerCase().replace(/g\s*[x×*]\s*(\d)/g, 'g$1').replace(/[^a-z0-9\u4e00-\u9fff]/g, '');
const PLATFORM = { jd: '京东', taobao: '淘宝/天猫', tb: '淘宝/天猫', pdd: '拼多多', tm: '天猫', dy: '抖音', ks: '快手', sn: '苏宁', '1': '淘宝/天猫', '2': '京东', '3': '拼多多', '4': '苏宁', '5': '唯品会', '7': '抖音', '8': '快手' };
const parseSales = (s) => { const m = String(s || '').match(/([\d.]+)\s*(万)?/); if (!m) return 0; const n = parseFloat(m[1]); return m[2] ? Math.round(n * 10000) : Math.round(n); };
const OBS = '2026-09-08T21:00:00+08:00';

// ---- catalog & 07-28 baseline ----
const CAT = {};
for (const c of ['cpu', 'gpu', 'motherboard', 'memory', 'ssd', 'psu', 'case', 'cooler']) {
  for (const line of fs.readFileSync(path.join(ROOT, 'scripts/data/parts', c + '.jsonl'), 'utf8').split(/\r?\n/)) {
    if (line.trim()) { const o = JSON.parse(line); CAT[o.sku] = o; }
  }
}
const oldBase = {};
for (const line of fs.readFileSync(path.join(ROOT, 'scripts/data/prices/2026-07-28.csv'), 'utf8').split(/\r?\n/).slice(1)) {
  if (!line.trim()) continue;
  const [sku, price, source, cap] = line.split(',');
  oldBase[sku] = { price, source, cap };
}

// ---- 复核决策表：accept = 买手价入基线 v3；否则 carry（原样沿用 07-28）----
// needle 为命中行标题的判别片段（squash 后子串），用于在候选中锁定复核选定的那一行。
const DEC = {
  // ===== CPU：16 accept / 4 carry =====
  'cpu-i5-12400f': { accept: 899, needle: '单核睿频至高', note: '京东自营单型号行' },
  'cpu-i5-13400f': { accept: 1233 },
  'cpu-i5-14600kf': { accept: 1499, needle: '14核20线程', note: '京东自营单型号行' },
  'cpu-i7-14700k': { accept: 2699 },
  'cpu-r5-5500': { accept: 599, needle: '锐龙5000系列', note: 'AMD京东自营' },
  'cpu-r5-5600': { accept: 799, needle: '畅玩无畏契约', note: 'AMD京东自营' },
  'cpu-r5-7500f': { accept: 829, needle: '畅玩打瓦', note: 'AMD京东自营；晨间 809 行被本行取代' },
  'cpu-r5-7600': { accept: 1492.7, needle: '智酷版', note: 'AMD京东自营 智酷版盒装' },
  'cpu-r5-7600x': { accept: 1799 },
  'cpu-r5-9600x': { accept: 1329 },
  'cpu-r7-5700x': { accept: 1399 },
  'cpu-r7-7700': { accept: 2399 },
  'cpu-r7-7800x3d': { accept: 2299, needle: '7800x3d盒装', note: '拒 Amazon 海外店 4155，取京东 R7 7800X3D 盒装行' },
  'cpu-r7-9700x': { accept: 1749 },
  'cpu-r7-9800x3d': { accept: 2969 },
  'cpu-r9-9900x': { accept: 2499 },
  'cpu-i3-12100f': { note: '仅散片(794)/多型号行；晨间 569 行为多型号链接变体不确定，回收为沿用' },
  'cpu-i5-12600k': { accept: 1499, needle: '12代i512600k盒装', note: '换词复扫晋升：Intel官方旗舰店 盒装行；KF 979 < K 1499 家族梯度正常；较2149.5 −30%（12代清货）' },
  'cpu-r9-7900': { note: '仅散片/多型号行，无盒装报价' },
  'cpu-ultra5-245k': { accept: 1219, needle: 'ultra5245k14核心14线程官方标配单u', note: '换词复扫晋升：英特尔集宇专卖店 官方标配单U行；散片1198/盒装1226第三方并存；较1299 −6%' },
  // ===== GPU：4 accept / 16 carry =====
  'gpu-asus-4070tis-tuf': { accept: 7479, needle: '4070tis', note: '尾部 TUF RTX4070TIS OC 盒装' },
  'gpu-asus-4080s-tuf': { accept: 8990, needle: '4080s', note: '尾部 TUF RTX4080S OC 工厂简包' },
  'gpu-gb-4070-windforce': { accept: 5050, needle: 'windforce', note: '风 force OC V2 全新单件' },
  'gpu-sapphire-7800xt-pulse': { accept: 5515.16, needle: 'rx7800xt16g白金', note: '尾部 蓝宝石 RX7800XT 16G 白金（小店，家族价格互证）' },
  'gpu-asus-4060ti-dual-evo': { note: '无 DUAL EVO 尾部命中行' },
  'gpu-gb-5060-windforce': { note: '买手搜索 0 结果（多关键词验证）' },
  'gpu-gb-5060ti-windforce-16g': { note: '无 WINDFORCE 16G 精确行' },
  'gpu-gb-5070-windforce-sff': { note: '无 SFF 精确行' },
  'gpu-gb-5080-gaming': { accept: 15099, needle: '5080gamingoc16g', note: '换词复扫晋升：技嘉电脑旗舰店 魔鹰 GAMING OC 官方行；超级雕 16538 梯度正常；较11577 +30%' },
  'gpu-intel-b580-le': { note: '仅 Tri/Photon 变体行，无 LE 版' },
  'gpu-msi-3060-ventus2x': { note: '行均为挡板/风扇配件或整机' },
  'gpu-msi-4060-ventus2x-black': { note: '行均为挡板/风扇配件或整机' },
  'gpu-msi-4070s-ventus2x': { note: '行均为挡板/风扇配件或整机' },
  'gpu-msi-5070ti-ventus3x': { accept: 9999, needle: 'rtx5070ti16g万图师oc', note: '换词复扫晋升：微星官方行 万图师OC（50系 Ventus 为 3X 款）；魔龙OC 10999 梯度正常；较6999 +43%' },
  'gpu-sapphire-6600-pulse': { note: '贴纸/风扇/二手/多型号行' },
  'gpu-sapphire-7600-pulse': { note: '贴纸/风扇/二手/多型号行' },
  'gpu-sapphire-7700xt-pulse': { accept: 3299, needle: 'rx7700xt12g白金版', note: '换词复扫晋升：蓝宝石旗舰店 白金版官方行；极地 3399 梯度正常；较3399 −3%' },
  'gpu-sapphire-7900xt-pulse': { note: '贴纸/风扇/二手/多型号行' },
  'gpu-sapphire-9060xt-pulse-16g': { accept: 4649, needle: '脉动rx9060xt16gboc', note: '换词复扫晋升：蓝宝石官方行；宽澜第三方 4589 互证；黑钻16G 4799 梯度正常；较3199 +45%' },
  'gpu-sapphire-9070xt-pulse': { note: '换词复扫仍无脉动精确行：6299 为无尾多型号行，6899 尾部为 7900XTX 白金（型号不符），8299 为氮动+电源套装；维持沿用' },
  // ===== MB：12 accept / 8 carry =====
  'mb-asus-b650e-f-strix': { accept: 1799 },
  'mb-gb-x870-eagle-wifi7': { accept: 1649, needle: '9700x', note: '技嘉京东自营' },
  'mb-msi-b550m-pro-vdh-wifi': { accept: 599 },
  'mb-msi-b650m-mortar-wifi': { accept: 1098, needle: 'magb650mmortarwifiddr5', note: '微星京东自营' },
  'mb-msi-b760m-a-wifi': { accept: 949, needle: 'prob760mawifi' },
  'mb-msi-b760m-a-wifi-ddr4': { accept: 929, needle: '主板支持cpu14400f', note: '微星京东自营 DDR4 II' },
  'mb-msi-b850-tomahawk-max': { accept: 1749 },
  'mb-msi-b860m-a-wifi': { accept: 1049 },
  'mb-msi-h610m-g-ddr4': { accept: 549 },
  'mb-msi-pro-a620m-e': { accept: 699 },
  'mb-msi-z790-p-wifi': { accept: 1499, needle: 'proz790pwifi' },
  'mb-msi-z890-tomahawk-wifi': { accept: 2573 },
  'mb-gb-b550-aorus-elite-v2': { note: '换词复扫仍仅 B550M 小雕系列（mATX）行，无 ATX V2 精确行' },
  'mb-gb-b650-aorus-elite-ax': { accept: 1899, needle: 'b650aoruseliteax主板ddr5', note: '换词复扫晋升：技嘉京东自营 ATX DDR5 精确行；较1239 +53%（AM5 板行情）' },
  'mb-gb-b650i-aorus-ultra': { note: '仅成新/板U套装' },
  'mb-msi-b550-tomahawk': { accept: 999, needle: 'b550tomahawk战斧导弹主板am4', note: '换词复扫晋升：齐鲁单店双行同价；AM4 板下行；高于 B550M 小雕 629 梯度正常；较1399 −29%' },
  'mb-gb-b760-gaming-x-ax': { accept: 750, needle: 'b760mgamingxaxddr4', note: '换词复扫晋升：齐鲁单店双行同价 DDR4 版；较1049 −29%（DDR4 板清货）' },
  'mb-msi-b860-tomahawk-wifi': { note: '换词复扫仍为 B860M 迫击炮/轰炸机混合行，无 ATX 战斧导弹精确行' },
  'mb-asrock-b650m-hdv-m2': { note: '仅挡板配件行' },
  'mb-msi-b650-tomahawk-wifi': { note: '换词复扫仍为 GAMING PLUS/EDGE 混合行，议价居多' },
  // ===== MEM：10 accept / 10 carry =====
  'mem-corsair-lpx-16-3200': { accept: 1149, needle: '16gb2条套条', note: '尾部 DDR4 3200 16GB 2条套条【8G×2】' },
  'mem-crucial-ballistix-16-3200': { accept: 569, needle: '铂胜', note: '尾部 铂胜 16G(8G*2) 3200' },
  'mem-kingston-beast-16-3200-d4': { accept: 1198, note: '尾部 野兽马甲条 8G×2 3200' },
  'mem-kingston-beast-32-3600-d4': { accept: 2198, note: '尾部 DDR4 3600 16G×2' },
  'mem-kingston-beast-32-5600': { accept: 3704.05, needle: '32g16g2', note: '尾部 5600频 32G(16G×2)' },
  'mem-kingston-beast-32-6000': { accept: 4299, note: '尾部 16G*2 6000MHz' },
  'mem-kingston-beast-16-5200': { accept: 2379, needle: '16gb8g2套装ddr55200', note: '京东自营 16GB(8G×2) 5200 非RGB' },
  'mem-kingston-beast-16-6000': { accept: 2499, needle: '超级野兽', note: '京东自营 16GB(8G×2) DDR5 6000' },
  'mem-gskill-flarex5-32-6000': { accept: 4299, needle: '16g26000', note: '焰锋戟 16G*2 6000 32G套装（标题含灯条，RGB 变体存疑）' },
  'mem-gskill-ripjawsv-16-3600': { accept: 949, note: '芝奇自营 16GB(8G×2) DDR4 3600 C16（RGB 灯条变体）' },
  'mem-corsair-lpx-32-3600': { accept: 1759, needle: '复仇者lpx游戏条c18', note: '换词复扫晋升：京东自营 32GB(16G×2) 3600 C18 黑色官方行（白色 1759 并存）；第三方 1689 互证；较429 +310% DDR4 现货行情' },
  'mem-corsair-veng-32-6000': { accept: 3799, needle: '复仇者d516g26000z36套装', note: '换词复扫晋升：海盗船Corsair旗舰 16G*2 6000 Z36 官方行；星宏禾3419/思瑞贝尔3689 互证；RGB 6800 4199 梯度正常；较699 +443% DDR5 行情' },
  'mem-corsair-veng-rgb-32-6000': { note: '套条行价格异常（约 0.24× 基线），不可信' },
  'mem-gskill-s5-32-6000': { note: '尾部为 6400 焰刃变体' },
  'mem-gskill-z5-rgb-32-6400': { note: '尾部不符（焰刃 M5 6400 等）' },
  'mem-gskill-z5neo-rgb-32-6000': { note: '买手搜索 0 结果' },
  'mem-adata-lancer-32-5200': { note: '无套条精确行' },
  'mem-team-delta-32-3200-d4': { note: '仅议价维修行' },
  'mem-team-delta-32-6000-white': { accept: 3799, needle: '600032g2x16gc30特挑海力士白', note: '换词复扫晋升：十铨科技旗舰店 白色 6000 C30 16G×2 官方行；6400 3999 梯度正常；较1199 +217% DDR5 行情' },
  'mem-gskill-ripjawsv-32-3200': { note: '买手搜索 0 结果' },
  // ===== SSD：9 accept / 11 carry =====
  'ssd-crucial-bx500-1tb': { accept: 549 },
  'ssd-crucial-mx500-1tb': { accept: 1599 },
  'ssd-crucial-t500-2tb': { accept: 2212.08, needle: '黑色2tb', note: '京东国际海外官方店（跨境渠道），尾部 黑色 2TB 精确；同店 T700/T705 混串行不采' },
  'ssd-kingston-a400-480gb': { accept: 593.1 },
  'ssd-kingston-kc3000-1tb': { accept: 1699, needle: '读速高达7000', note: '金士顿京东自营' },
  'ssd-kingston-nv2-1tb': { accept: 709, needle: 'nv2', note: '尾部 NV2系列 1TB（拒 NV3 尾部行）' },
  'ssd-samsung-990pro-2tb': { accept: 2699 },
  'ssd-wd-sa510-1tb': { accept: 1699, needle: 'wds100t3b0a', note: '尾部 蓝盘SA510 1TB（WDS100T3B0A）' },
  'ssd-wd-sn850x-2tb': { accept: 2599, needle: 'wds200t2x0e', note: '尾部 SN850X-2TB（WDS200T2X0E），与 990PRO 2TB 2699 互证' },
  'ssd-crucial-p3plus-1tb': { note: '仅议价/维修行' },
  'ssd-crucial-p5plus-1tb': { note: '仅 P510/移动硬盘行' },
  'ssd-intel-670p-1tb': { note: '仅议价行' },
  'ssd-samsung-870evo-1tb': { accept: 1089, needle: '870evo固态硬盘全新1tb', note: '换词复扫晋升：华宸电竞 870EVO 1TB 精确行（专卖店 999 为多型号混串不采）；NAND 行情 较629 +73%' },
  'ssd-samsung-870qvo-2tb': { note: '询价/议价/异常价行' },
  'ssd-samsung-970evoplus-1tb': { note: '行均不符' },
  'ssd-samsung-980pro-1tb': { note: '仅 PM980PRO 杂牌/检修行' },
  'ssd-wd-sn580-1tb': { note: '无 SN580 尾部行（SN5100/SN7100 等）' },
  'ssd-wd-sn770-1tb': { note: '全部拆机/二手/议价行' },
  'ssd-adata-legend800-1tb': { note: '仅移动硬盘等无关行' },
  'ssd-adata-s70blade-1tb': { note: '仅代拍/无关行' },
  // ===== PSU：14 accept / 6 carry =====
  'psu-bq-pp12m-850': { accept: 1068 },
  'psu-corsair-hx1000i-2022': { accept: 1779, needle: 'hx1000i1000w', note: '尾部 HX1000i 1000W' },
  'psu-corsair-rm1000x-2021': { accept: 1199, needle: '磁悬浮风扇', note: '拒 AIR5400 机箱+电源套装 2999，取 RM1000x 1000W 单件' },
  'psu-corsair-rm750e': { accept: 899, needle: 'rm750e额定750w' },
  'psu-corsair-rm850e': { accept: 839, needle: 'rm850e额定850w' },
  'psu-corsair-rm850x-2021': { accept: 1049, needle: 'rm850x850w电源', note: '京东自营 RM850x 850W 单型号行' },
  'psu-gb-ud850gm-pg5': { accept: 1099, needle: 'ud850gmpg5', note: '取标准色 UD850GM PG5【850W/全模组】1099；白色 719 行存疑只入学习行' },
  'psu-msi-mag-a650bn': { accept: 279, manual: true, note: '晨间买手行：A650BN迫击炮额定650W 铜牌（全量扫库仅 A650BNL 变体）' },
  'psu-msi-mag-a750gl': { accept: 1047.78 },
  'psu-msi-mag-a850gl': { accept: 977.62, note: '拒 539 异常行，取全名 A850GL 精确行' },
  'psu-seasonic-focus-gx850-atx30': { accept: 1099, needle: 'gx850v5' },
  'psu-seasonic-vertex-gx1000': { accept: 1549, needle: 'vertexgx1000' },
  'psu-tt-gf3-750': { accept: 599, needle: 'gf3750wargb', note: 'ARGB 变体（449 行触发 suspicious_low 弃用）' },
  'psu-tt-gf3-850': { accept: 699, needle: 'gf3850wargb', note: 'ARGB 变体' },
  'psu-bq-pp12m-750': { note: '全部无关行' },
  'psu-bq-sp12-750': { note: '全部无关行' },
  'psu-bq-sp12-1000': { note: '全部无关行' },
  'psu-corsair-cx650m': { note: '仅模组线/玩具行' },
  'psu-msi-mpg-a850g': { note: '仅模组线行' },
  'psu-seasonic-focus-gx750-atx30': { note: 'GX750 行价格异常低（疑似变体错绑）' },
  // ===== CASE：7 accept / 13 carry =====
  'case-asus-prime-ap201': { accept: 412, needle: 'ap201冰立方', note: '拒亚马逊海外 1073 越界行' },
  'case-coolermaster-nr200p': { accept: 699, needle: 'nr200pv3黑色', note: 'V3 现行版' },
  'case-coolermaster-td500-mesh-v2': { accept: 499, needle: 'td500meshv2白色中塔' },
  'case-fractal-north': { accept: 999, needle: 'north黑色网孔版' },
  'case-fractal-terra': { accept: 1449, needle: '台式机电脑黑色' },
  'case-fractal-torrent': { accept: 1349, needle: '黑色金属版' },
  'case-lianli-a3-matx': { accept: 499, needle: 'a3木头版机箱黑色', note: '取黑色木头版标准款；白色 549 入学习行' },
  'case-bequiet-pure-base-500dx': { note: '全部无关行' },
  'case-bequiet-shadow-base-800-fx': { accept: 1444, needle: 'shadowbase800fx', note: '换词复扫晋升：bequiet 官方淘宝行；800DX 1274 为不同型号；较1899 −24% 清货' },
  'case-corsair-4000d-airflow': { note: '仅 4000D RGB AF 变体/套装' },
  'case-corsair-5000d-airflow': { note: '仅 5000D RGB/CORE 变体' },
  'case-fractal-define-7': { note: '仅 Define 7 C 变体' },
  'case-fractal-meshify-2-compact': { note: '全部无关行' },
  'case-fractal-pop-air': { accept: 649, needle: 'popair全黑化cleartg', note: '换词复扫晋升：Fractal Design 京东官方行 Clear TG 变体；RGB 699 并存；较599 +8%' },
  'case-lianli-lancool-216': { accept: 579, needle: '一体式网孔面板联力鬼斧l216黑色', note: '换词复扫晋升：联力旗舰店 L216（鬼斧216=LANCOOL 216）黑色官方行；知行数码 632.4 互证；较599 −3%' },
  'case-lianli-o11-dynamic-evo': { accept: 1099, needle: '包豪斯o11devo台式机游戏全侧透明eatx电脑水冷机箱', note: '换词复扫晋升：plain EVO 仅摩西单店行；官方行均为 EVO RGB 黑1119/白1219 与全视版 549 起，款式已分化；较799 +38%' },
  'case-montech-air-903-max': { note: '全部无关行' },
  'case-nzxt-h5-flow-2024': { note: '日本直邮进口渠道价，不代表行货' },
  'case-nzxt-h6-flow-2023': { note: '全部无关行' },
  'case-nzxt-h7-flow-2024': { note: '日本直邮进口渠道价，不代表行货' },
  // ===== COOLER：10 accept / 10 carry =====
  'cooler-arctic-lf3-240': { accept: 905.3, needle: 'liquidfreezeriii240argb', note: 'ARGB 版' },
  'cooler-arctic-lf3-360': { accept: 584, needle: 'iiipro360黑色', note: 'III Pro 黑色版' },
  'cooler-bequiet-dark-rock-pro-5': { accept: 569 },
  'cooler-coolermaster-hyper212-black': { accept: 54.5 },
  'cooler-deepcool-ak620': { accept: 482.24, needle: 'ak620标准版', note: '京东自营 AK620标准版；淘宝 239 行入学习行' },
  'cooler-deepcool-assassin-iv': { accept: 529, needle: '快拆风扇assassiniv', note: '黑色版（拒尾缀 WH 白色行）' },
  'cooler-noctua-nh-u12s': { accept: 480, needle: '猫头鹰nhu12s' },
  'cooler-thermalright-frozen-prism-240': { accept: 335, needle: 'frozenprism240一体式' },
  'cooler-thermalright-pa120se': { accept: 132.47, needle: '双塔cpu散热器无光', note: '无光标准版（晨间 136.88 行被本行取代）' },
  'cooler-thermalright-ps120se': { accept: 132.47, needle: 'ps120se幻灵', note: '复核改选：拒 EVO/DIGITAL 尾部行，取 PS120 SE 幻灵标准版' },
  'cooler-arctic-lf2-240': { note: '全部无关行' },
  'cooler-bequiet-dark-rock-pro-4': { note: '全部无关行' },
  'cooler-corsair-h100i-elite-capellix-xt': { note: '无有效行' },
  'cooler-corsair-h150i-elite-capellix-xt': { note: '240/360 混合尾部' },
  'cooler-deepcool-ag400': { accept: 79, needle: '玄冰ag400性能版cpu散热器4热管', note: '换词复扫晋升：AG400性能版多店一致79（酷风/零度世家/辉煌）；G2 56.9 为新代型号不采；较66.9 +18%' },
  'cooler-deepcool-lt520': { accept: 559.52, needle: '冰魔方240lt520', note: '换词复扫晋升：唯一精确行（云禾单店双行同 listing）；较389 +44% 单源' },
  'cooler-msi-mag-coreliquid-e360': { note: '无有效行' },
  'cooler-nzxt-kraken-240': { note: '仅多型号/进口直邮行' },
  'cooler-nzxt-kraken-x63': { note: '无 X63 尾部行' },
  'cooler-noctua-nh-d15': { accept: 699, needle: 'atx机箱', note: '换词复扫晋升：NOCTUA散热器旗舰店 NH-D15 CH.BK 标准版官方行；chromax 799/G2 1199 梯度正常；与基线持平' },
};

// 晨间买手人工保留行（psu-msi-mag-a650bn）
const MANUAL_ROWS = {
  'psu-msi-mag-a650bn': {
    title: 'MSI/微星A650BN迫击炮额定650W铜牌电源主机电脑全模组750W电源',
    price: 279, orig: 329, coupon: 0, platform: '淘宝/天猫', shop: '河星电子',
    goodsId: 'N0pAbNAH5tGqX9B2mmsZRbHRtB-QoryeYWUbJA5ZAMgSbj', monthSales: 600, flags: [],
  },
};

const JUNK = ['拆机', '二手', '准新', '坏', '维修', '回收', '出租', '样品', '询价', '议价', '成新', '套装', '板u', '整机', '矿卡'];
const JUNK_CASE_OK = ['主机'];

const toCand = (r) => ({
  title: r.title, price: parseFloat(r.actualPrice), orig: parseFloat(r.originalPrice) || null,
  coupon: parseFloat(r.couponPrice) || 0, platform: r.source, shop: r.shopName,
  goodsId: r.goodsId, monthSales: parseSales(r.monthSales), flags: [],
});

// ---- 解析 accept 行 ----
const problems = [];
function resolveAccept(sku, d) {
  if (d.manual) return MANUAL_ROWS[sku];
  const price = d.accept;
  // 同价 + 同标题的行视为同一 logical listing（同一链接被搜索返回多次 / 同店多 goodsId 重复上架）
  const logical = (rows) => {
    const seen = new Set();
    return rows.filter((c) => { const k = c.price.toFixed(2) + '|' + sq(c.title); if (seen.has(k)) return false; seen.add(k); return true; });
  };
  const inCands = logical(((CANDS[sku] && CANDS[sku].cands) || []).filter((c) => Math.abs(c.price - price) < 0.005 && (!d.needle || sq(c.title).includes(sq(d.needle)))));
  if (inCands.length === 1) return inCands[0];
  if (inCands.length > 1) { problems.push(`${sku}: ${inCands.length} 行命中 price=${price} needle=${d.needle || '-'}，需更精确 needle`); return null; }
  const raws = logical(((RESULTS[sku] && RESULTS[sku].rows) || []).map(toCand)
    .filter((c) => Number.isFinite(c.price) && Math.abs(c.price - price) < 0.005 && (!d.needle || sq(c.title).includes(sq(d.needle)))));
  if (raws.length === 1) return raws[0];
  if (raws.length > 1) { problems.push(`${sku}: raws ${raws.length} 行命中 price=${price} needle=${d.needle || '-'}`); return null; }
  problems.push(`${sku}: 未找到 accept 行 price=${price} needle=${d.needle || '-'}`);
  return null;
}

// ---- 学习行构造 ----
const outRows = [];
let rid = 0;
const accepts = {}; // sku -> accepted row
function offerCond(sku, title) {
  const s = sq(title);
  const cat = sku.split('-')[0];
  let offer = 'retail', cond = 'new';
  if (/散片|tray/.test(s)) offer = 'cpu_tray';
  if (/议价|询价/.test(s)) offer = 'inquiry_only';
  if (/工包/.test(s)) offer = 'bulk';
  if (/套装|搭|板u/.test(s)) offer = 'bundle';
  if (/二手|拆机|成新|准新|翻新|回收/.test(s)) cond = 'used';
  if (cat === 'cpu' && offer === 'retail') offer = 'cpu_only';
  return { offer, cond };
}
function emit(sku, row, decision, dnote, extraFlags) {
  const cat0 = sku.split('-')[0];
  const catName = { cpu: 'cpu', gpu: 'gpu', mb: 'motherboard', mem: 'memory', ssd: 'ssd', psu: 'psu', case: 'case', cooler: 'cooler' }[cat0] || cat0;
  const p = CAT[sku] || {};
  const { offer, cond } = offerCond(sku, row.title);
  const flags = [...(row.flags || []), ...(extraFlags || [])];
  const junkHit = JUNK.filter((j) => sq(row.title).includes(sq(j)) && !(cat0 === 'case' && JUNK_CASE_OK.includes(j)));
  const lowConf = decision === 'carry_evidence' || junkHit.length > 0 || flags.includes('suspicious_low');
  const conf = decision === 'accept' ? (flags.includes('multi_model_listing') ? 'medium' : 'high') : (lowConf ? 'low' : 'medium');
  outRows.push({
    row_id: 'lp2-' + String(++rid).padStart(3, '0'),
    target: sku, model: p.brand ? `${p.brand} ${p.model}` : (p.model || ''), category: p.category || catName,
    offer_type: offer, condition: cond,
    price_cny: row.price, original_cny: row.orig ?? null, coupon_cny: row.coupon ?? 0,
    platform: PLATFORM[row.platform] || row.platform || '', seller: row.shop || '',
    seller_type: /自营/.test(row.shop || '') ? 'self_operated' : 'third_party',
    listing_title: row.title, listing_ref: row.goodsId ? 'maishou:' + row.goodsId : '',
    monthly_sales: (row.monthSales || 0) + '+',
    confidence: conf, flags,
    note: dnote || '', decision, decision_note: dnote || '',
    source_type: 'aggregator_secondary', source: 'maishou88-skill', observed_at: OBS,
  });
}

for (const sku of Object.keys(CAT).sort()) {
  const d = DEC[sku];
  if (!d) { problems.push(`${sku}: 决策表缺项`); continue; }
  const isAccept = 'accept' in d;
  const note = d.note || (isAccept ? '目标型号行命中，选为基线价' : '');
  const emitted = new Set();
  if (isAccept) {
    const row = resolveAccept(sku, d);
    if (!row) continue;
    accepts[sku] = row;
    emit(sku, row, 'accept', note, ['baseline_accepted']);
    emitted.add(row.goodsId);
  }
  for (const c of (CANDS[sku] && CANDS[sku].cands) || []) {
    if (emitted.has(c.goodsId)) continue;
    emitted.add(c.goodsId);
    emit(sku, c, 'candidate_reject', isAccept ? '同 SKU 其他候选，未选为基线' : note, []);
  }
  if (!isAccept) {
    const rows = ((RESULTS[sku] && RESULTS[sku].rows) || []).map(toCand)
      .filter((c) => Number.isFinite(c.price) && c.price > 0)
      .sort((a, b) => a.price - b.price).slice(0, 6);
    const cat0 = sku.split('-')[0];
    for (const c of rows) {
      if (emitted.has(c.goodsId)) continue;
      emitted.add(c.goodsId);
      const st = sq(c.title);
      const junkHit = JUNK.some((j) => st.includes(sq(j)) && !(cat0 === 'case' && JUNK_CASE_OK.includes(j)));
      const old = oldBase[sku] ? parseFloat(oldBase[sku].price) : null;
      const mult = cat0 === 'mem' ? 6 : cat0 === 'gpu' ? 4 : 3;
      let extra = ['unmatched_listing'];
      if (junkHit) extra = ['junk_listing'];
      else if (old && c.price < old * 0.7) extra = ['suspicious_low'];
      else if (old && c.price > old * mult) extra = ['out_of_bounds'];
      emit(sku, c, 'carry_evidence', note, extra);
    }
  }
}

// ---- 基线 v3 CSV ----
const csvLines = ['sku,price_cny,source,captured_at'];
let nAccept = 0, nCarry = 0;
for (const sku of Object.keys(CAT).sort()) {
  const d = DEC[sku];
  if (!d) continue;
  if ('accept' in d && accepts[sku]) {
    csvLines.push(`${sku},${d.accept.toFixed(2)},maishou88,2026-09-08`);
    nAccept++;
  } else {
    const o = oldBase[sku];
    if (!o) { problems.push(`${sku}: 07-28 基线缺行，无法沿用`); continue; }
    csvLines.push(`${sku},${o.price},${o.source},${o.cap}`);
    nCarry++;
  }
}

if (problems.length) throw new Error(problems.join('\n'));
if (nAccept !== 102 || nCarry !== 58 || csvLines.length !== 161) throw new Error('Batch counts changed; review decisions.');
fs.mkdirSync(OUTPUT, { recursive: true });
fs.writeFileSync(path.join(OUTPUT, 'learning-price-rows-full-20260908.jsonl'),
  outRows.map((r) => JSON.stringify(r)).join('\n') + '\n');
fs.writeFileSync(path.join(OUTPUT, '2026-09-08.csv'), csvLines.join('\n') + '\n');

console.log(`accept=${nAccept} carry=${nCarry} total=${nAccept + nCarry} learning_rows=${outRows.length}`);
const counts = {};
for (const r of outRows) counts[r.decision] = (counts[r.decision] || 0) + 1;
console.log(JSON.stringify(counts));
if (problems.length) { console.log('PROBLEMS:'); for (const p of problems) console.log(' -', p); }
else console.log('no problems');
