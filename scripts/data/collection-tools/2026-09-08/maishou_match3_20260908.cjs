const fs = require('fs');
const path = require('path');
const { ROOT, DATA, OUTPUT } = require('./paths.cjs');

const RESULTS = path.join(DATA, 'maishou_merged_20260908.json');
const OLDCSV = path.join(ROOT, 'scripts', 'data', 'prices', '2026-07-28.csv');
const OUT = path.join(DATA, 'maishou_match_candidates3_20260908.json');

// ---------- 文本规范化：小写，去非字母数字汉字；8Gx2/8G×2/8G*2 -> 8g2 ----------
function squash(s) {
  return String(s || '')
    .toLowerCase()
    .replace(/g\s*[x×*]\s*(\d)/g, 'g$1')
    .replace(/[^a-z0-9\u4e00-\u9fff]/g, '');
}

const PLATFORM = { 1: 'taobao', 2: 'jd', 3: 'pdd', 4: 'suning', 5: 'vip', 7: 'douyin', 8: 'kuaishou' };
const G = (...alts) => alts; // 一个必含词组（组内任一命中即可）
const JUNK = ['拆机', '二手', '准新', '坏', '维修', '回收', '出租', '样品', '询价', '议价', '成新', '套装', '板u', '整机', '矿卡'];
const JUNK_CASE_OK = ['主机']; // 机箱标题常含“主机箱”，仅对非机箱类禁用
const SOFT = { 散片: ['tray_price', -1.5], 工包: ['bulk_pack', -2], 水货: ['parallel_import', -2] };

// 多型号堆砌检测：标题同时命中同品类多个不同型号 → 挂 multi_model_listing 重罚
const FAM = {
  cpu: ['12100f', '12400', '12490f', '12600k', '13100', '13400', '14100', '14400f', '14600k', '14700k', '5500', '5600', '5600x', '5600g', '5700x', '5700x3d', '5800x', '7400f', '7500f', '7600', '7600x', '7700', '7800x3d', '9600x', '9700x', '9800x3d', '9900x', '9950x', '7900', '7900x', '7950x', '245k', '245kf', '265k', '265kf', '285k', '225k'],
  gpu: ['3060', '4060', '4060ti', '4070', '4070ti', '4070super', '4080', '5060', '5060ti', '5070', '5070ti', '5080', '5090', '6600', '6650xt', '6750gre', '7600', '7600xt', '7700xt', '7800xt', '7900xt', '7900xtx', '9060xt', '9070', '9070xt', 'b580', 'a770'],
  mb: ['h610', 'b660', 'b660m', 'b760', 'b760m', 'z690', 'z790', 'a520', 'b550', 'b550m', 'x570', 'b650', 'b650m', 'b650e', 'x670e', 'b850', 'x870', 'x870e', 'a620', 'b860', 'b860m', 'z890'],
  mem: ['4g2', '8g2', '16g2', '24g2', '32g2', '48g2', '96g2'],
  ssd: ['240g', '250g', '256g', '480g', '500g', '512g', '1t', '2t', '4t'],
  psu: ['450w', '550w', '650w', '750w', '850w', '1000w', '1200w', '1500w', '1600w', 'hx1000i', 'hx1200i', 'hx1500i', 'rm750e', 'rm850e', 'rm850x', 'rm1000x', 'gx650', 'gx750', 'gx850', 'gx1000', 'cx650m', 'cx750m', 'a550bn', 'a650bn', 'a750gl', 'a850gl', 'a850g'],
  cooler: ['lf2', 'lf3', 'nhd15', 'd15s', 'nhu12s', 'u12a', 'ak620', 'ak400', 'ag400', 'ag300', 'ag620', 'lt520', 'lt720', 'pa120', 'ps120', 'assassin', 'prism', 'hyper212', '玄冰400', '冰魔方', 'darkrock', 'purerock', 'kraken', 'capellix', 'h100i', 'h150i', 'h60', 'h80i', 'krakenx63', 'kraken240', 'coreliquid', 'e360', 't610p', 't400i'],
  case: ['4000d', '4000x', '4000airflow', '5000d', '5000t', '5000x', 'nr200', 'h5flow', 'h6flow', 'h7flow', 'h710', 'o11', 'q58', 'a3matx', 'lancool', 'define7', 'meshify', 'torrent', 'terra', 'north', 'popair', 'ap201', 'td500', 'td300', '903max', 'purebase', 'shadowbase'],
};

// ---------- 逐 SKU 匹配规则：req = AND(组)，组内 OR；ban = 任一命中即排除 ----------
const RULES = {
  // CPU
  'cpu-i3-12100f': { req: [G('12100f')] },
  'cpu-i5-12400f': { req: [G('12400f')] },
  'cpu-i5-12600k': { req: [G('12600k')], ban: ['12600kf'] },
  'cpu-i5-13400f': { req: [G('13400f')] },
  'cpu-i5-14600kf': { req: [G('14600kf')] },
  'cpu-i7-14700k': { req: [G('14700k')], ban: ['14700kf'] },
  'cpu-r5-5500': { req: [G('5500'), G('锐龙', 'amd', 'ryzen', 'r5')], ban: ['5500xt'] },
  'cpu-r5-5600': { req: [G('5600'), G('锐龙', 'amd', 'ryzen', 'r5')], ban: ['5600x', '5600g', '5600xt'] },
  'cpu-r5-7500f': { req: [G('7500f'), G('锐龙', 'amd', 'ryzen', 'r5')] },
  'cpu-r5-7600': { req: [G('7600'), G('锐龙', 'amd', 'ryzen', 'r5')], ban: ['7600x'] },
  'cpu-r5-7600x': { req: [G('7600x'), G('锐龙', 'amd', 'ryzen', 'r5')] },
  'cpu-r5-9600x': { req: [G('9600x'), G('锐龙', 'amd', 'ryzen', 'r5')] },
  'cpu-r7-5700x': { req: [G('5700x'), G('锐龙', 'amd', 'ryzen', 'r7')], ban: ['5700x3d'] },
  'cpu-r7-7700': { req: [G('7700'), G('锐龙', 'amd', 'ryzen', 'r7')], ban: ['7700x'] },
  'cpu-r7-7800x3d': { req: [G('7800x3d'), G('锐龙', 'amd', 'ryzen', 'r7')] },
  'cpu-r7-9700x': { req: [G('9700x'), G('锐龙', 'amd', 'ryzen', 'r7')] },
  'cpu-r7-9800x3d': { req: [G('9800x3d'), G('锐龙', 'amd', 'ryzen', 'r7')] },
  'cpu-r9-7900': { req: [G('7900'), G('锐龙', 'amd', 'ryzen', 'r9')], ban: ['7900x'] },
  'cpu-r9-9900x': { req: [G('9900x'), G('锐龙', 'amd', 'ryzen', 'r9')] },
  'cpu-ultra5-245k': { req: [G('245k')], ban: ['245kf'] },
  // GPU
  'gpu-asus-4060ti-dual-evo': { req: [G('4060ti'), G('dual')], vram: true },
  'gpu-asus-4070tis-tuf': { req: [G('4070tisuper', '4070tis'), G('tuf')] },
  'gpu-asus-4080s-tuf': { req: [G('4080super', '4080s'), G('tuf')] },
  'gpu-gb-4070-windforce': { req: [G('4070'), G('windforce')], ban: ['4070ti', 'super', '5070'] },
  'gpu-gb-5060-windforce': { req: [G('5060'), G('windforce', '风神')], ban: ['5060ti'] },
  'gpu-gb-5060ti-windforce-16g': { req: [G('5060ti'), G('windforce', '风神'), G('16g')], ban: ['8g'] },
  'gpu-gb-5070-windforce-sff': { req: [G('5070'), G('windforce')], ban: ['5070ti'] },
  'gpu-gb-5080-gaming': { req: [G('5080'), G('gaming')], ban: ['aorus', 'windforce', 'master', 'eagle', 'vision', 'aero', 'xtreme'] },
  'gpu-intel-b580-le': { req: [G('b580'), G('le', 'limited')], ban: ['tri', 'index'] },
  'gpu-msi-3060-ventus2x': { req: [G('3060'), G('ventus', '万图师')], ban: ['3060ti'], vram: true },
  'gpu-msi-4060-ventus2x-black': { req: [G('4060'), G('ventus'), G('black')], ban: ['4060ti'] },
  'gpu-msi-4070s-ventus2x': { req: [G('4070super', '4070s'), G('ventus')], ban: ['4070ti'] },
  'gpu-msi-5070ti-ventus3x': { req: [G('5070ti'), G('ventus', '万图师')] },
  'gpu-sapphire-6600-pulse': { req: [G('6600'), G('白金', 'pulse')], ban: ['极地'], ban: ['6600xt'] },
  'gpu-sapphire-7600-pulse': { req: [G('7600'), G('白金', 'pulse')], ban: ['极地'], ban: ['7600xt'] },
  'gpu-sapphire-7700xt-pulse': { req: [G('7700xt'), G('白金', 'pulse')], ban: ['极地'] },
  'gpu-sapphire-7800xt-pulse': { req: [G('7800xt'), G('白金', 'pulse')], ban: ['极地'] },
  'gpu-sapphire-7900xt-pulse': { req: [G('7900xt'), G('白金', 'pulse')], ban: ['极地'], ban: ['7900xtx'] },
  'gpu-sapphire-9060xt-pulse-16g': { req: [G('9060xt'), G('白金', 'pulse'), G('16g')], ban: ['8g'] },
  'gpu-sapphire-9070xt-pulse': { req: [G('9070xt'), G('白金', 'pulse')], ban: ['极地'] },
  // 主板（AM4=B550 全 DDR4、AM5/1851 全 DDR5 无需约束；1700 平台区分 DDR4/DDR5）
  'mb-asrock-b650m-hdv-m2': { req: [G('b650m'), G('hdv')] },
  'mb-asus-b650e-f-strix': { req: [G('b650ef'), G('strix', '猛禽')] },
  'mb-gb-b550-aorus-elite-v2': { req: [G('b550'), G('aoruselite')], ban: ['b550m', 'ax'] },
  'mb-gb-b650-aorus-elite-ax': { req: [G('b650'), G('aoruselite'), G('ax')], ban: ['b650m', 'b650e', 'v2', 'ice'] },
  'mb-gb-b650i-aorus-ultra': { req: [G('b650i'), G('aorusultra', 'ultra')] },
  'mb-gb-b760-gaming-x-ax': { req: [G('b760'), G('gamingx'), G('ax')], ban: ['b760m', 'ddr4'] },
  'mb-gb-x870-eagle-wifi7': { req: [G('x870eagle'), G('wifi7')] },
  'mb-msi-b550-tomahawk': { req: [G('b550'), G('tomahawk', '战斧')], ban: ['b550m'] },
  'mb-msi-b550m-pro-vdh-wifi': { req: [G('b550m'), G('provdh'), G('wifi')] },
  'mb-msi-b650-tomahawk-wifi': { req: [G('b650'), G('tomahawk', '战斧'), G('wifi')], ban: ['b650m'] },
  'mb-msi-b650m-mortar-wifi': { req: [G('b650m'), G('mortar', '迫击炮'), G('wifi')], ban: ['max'] },
  'mb-msi-b760m-a-wifi': { req: [G('b760ma'), G('wifi')], ban: ['ddr4'] },
  'mb-msi-b760m-a-wifi-ddr4': { req: [G('b760ma'), G('wifi'), G('ddr4')] },
  'mb-msi-b850-tomahawk-max': { req: [G('b850'), G('tomahawk', '战斧')] },
  'mb-msi-b860-tomahawk-wifi': { req: [G('b860'), G('tomahawk', '战斧')], ban: ['b860m'] },
  'mb-msi-b860m-a-wifi': { req: [G('b860ma'), G('wifi')] },
  'mb-msi-h610m-g-ddr4': { req: [G('h610mg'), G('ddr4')] },
  'mb-msi-pro-a620m-e': { req: [G('a620me')] },
  'mb-msi-z790-p-wifi': { req: [G('z790p'), G('wifi')], ban: ['ddr4'] },
  'mb-msi-z890-tomahawk-wifi': { req: [G('z890'), G('tomahawk', '战斧')] },
  // 内存：代数+频率+容量粒度+产品线 全匹配
  'mem-adata-lancer-32-5200': { req: [G('lancer'), G('ddr5'), G('5200'), G('16g2')], ban: ['rgb'] },
  'mem-corsair-lpx-16-3200': { req: [G('lpx'), G('ddr4'), G('3200'), G('8g2')], ban: ['16g2'] },
  'mem-corsair-lpx-32-3600': { req: [G('lpx'), G('ddr4'), G('3600'), G('16g2')], ban: ['32g2'] },
  'mem-corsair-veng-32-6000': { req: [G('复仇者', 'vengeance'), G('ddr5'), G('6000'), G('16g2')], ban: ['rgb', 'lpx'] },
  'mem-corsair-veng-rgb-32-6000': { req: [G('复仇者', 'vengeance'), G('ddr5'), G('6000'), G('16g2'), G('rgb')], ban: ['lpx'] },
  'mem-crucial-ballistix-16-3200': { req: [G('铂胜', 'ballistix'), G('ddr4'), G('3200'), G('8g2')] },
  'mem-gskill-flarex5-32-6000': { req: [G('焰锋戟', 'flarex5'), G('ddr5'), G('6000'), G('16g2')], ban: ['rgb'] },
  'mem-gskill-ripjawsv-16-3600': { req: [G('焰光戟', 'ripjaws'), G('ddr4'), G('3600'), G('8g2')] },
  'mem-gskill-ripjawsv-32-3200': { req: [G('焰光戟', 'ripjaws'), G('ddr4'), G('3200'), G('16g2')] },
  'mem-gskill-s5-32-6000': { req: [G('幻锋戟', 'tridentz5'), G('ddr5'), G('6000'), G('16g2')], ban: ['neo', 'rgb'] },
  'mem-gskill-z5-rgb-32-6400': { req: [G('幻锋戟', 'tridentz5'), G('ddr5'), G('6400'), G('16g2'), G('rgb')], ban: ['neo'] },
  'mem-gskill-z5neo-rgb-32-6000': { req: [G('neo'), G('幻锋戟', 'tridentz5'), G('ddr5'), G('6000'), G('16g2'), G('rgb')] },
  'mem-kingston-beast-16-3200-d4': { req: [G('野兽', 'beast', 'fury'), G('ddr4'), G('3200'), G('8g2')] },
  'mem-kingston-beast-16-5200': { req: [G('野兽', 'beast', 'fury'), G('ddr5'), G('5200'), G('8g2')] },
  'mem-kingston-beast-16-6000': { req: [G('野兽', 'beast', 'fury'), G('ddr5'), G('6000'), G('8g2')], ban: ['16g2'] },
  'mem-kingston-beast-32-3600-d4': { req: [G('野兽', 'beast', 'fury'), G('ddr4'), G('3600'), G('16g2')] },
  'mem-kingston-beast-32-5600': { req: [G('野兽', 'beast', 'fury'), G('ddr5'), G('5600'), G('16g2')] },
  'mem-kingston-beast-32-6000': { req: [G('野兽', 'beast', 'fury'), G('ddr5'), G('6000'), G('16g2')], ban: ['8g2'] },
  'mem-team-delta-32-3200-d4': { req: [G('delta'), G('十铨', 'team'), G('ddr4'), G('3200'), G('16g2')] },
  'mem-team-delta-32-6000-white': { req: [G('delta'), G('十铨', 'team'), G('ddr5'), G('6000'), G('16g2'), G('白', 'white')] },
  // SSD
  'ssd-adata-legend800-1tb': { req: [G('legend800'), G('1tb')] },
  'ssd-adata-s70blade-1tb': { req: [G('s70blade'), G('1tb')] },
  'ssd-crucial-bx500-1tb': { req: [G('bx500'), G('1tb')] },
  'ssd-crucial-mx500-1tb': { req: [G('mx500'), G('1tb')] },
  'ssd-crucial-p3plus-1tb': { req: [G('p3plus'), G('1tb')] },
  'ssd-crucial-p5plus-1tb': { req: [G('p5plus'), G('1tb')] },
  'ssd-crucial-t500-2tb': { req: [G('t500'), G('2tb')] },
  'ssd-intel-670p-1tb': { req: [G('670p'), G('1tb')] },
  'ssd-kingston-a400-480gb': { req: [G('a400'), G('480g')] },
  'ssd-kingston-kc3000-1tb': { req: [G('kc3000'), G('1tb')] },
  'ssd-kingston-nv2-1tb': { req: [G('nv2'), G('1tb')] },
  'ssd-samsung-870evo-1tb': { req: [G('870evo'), G('1tb')] },
  'ssd-samsung-870qvo-2tb': { req: [G('870qvo'), G('2tb')] },
  'ssd-samsung-970evoplus-1tb': { req: [G('970evoplus'), G('1tb')] },
  'ssd-samsung-980pro-1tb': { req: [G('980pro'), G('1tb')] },
  'ssd-samsung-990pro-2tb': { req: [G('990pro'), G('2tb')] },
  'ssd-wd-sa510-1tb': { req: [G('sa510'), G('1tb')] },
  'ssd-wd-sn580-1tb': { req: [G('sn580'), G('1tb')] },
  'ssd-wd-sn770-1tb': { req: [G('sn770'), G('1tb')] },
  'ssd-wd-sn850x-2tb': { req: [G('sn850x'), G('2tb')] },
  // 电源
  'psu-bq-pp12m-750': { req: [G('purepower12', 'pp12m'), G('750w')] },
  'psu-bq-pp12m-850': { req: [G('purepower12', 'pp12m'), G('850w')] },
  'psu-bq-sp12-1000': { req: [G('straightpower12', 'sp12'), G('1000w')] },
  'psu-bq-sp12-750': { req: [G('straightpower12', 'sp12'), G('750w')] },
  'psu-corsair-cx650m': { req: [G('cx650m')] },
  'psu-corsair-hx1000i-2022': { req: [G('hx1000i')] },
  'psu-corsair-rm1000x-2021': { req: [G('rm1000x')], ban: ['shift'] },
  'psu-corsair-rm750e': { req: [G('rm750e')] },
  'psu-corsair-rm850e': { req: [G('rm850e')] },
  'psu-corsair-rm850x-2021': { req: [G('rm850x')], ban: ['shift'] },
  'psu-gb-ud850gm-pg5': { req: [G('ud850gm'), G('pg5')] },
  'psu-msi-mag-a650bn': { req: [G('a650bn')], ban: ['bnl'] },
  'psu-msi-mag-a750gl': { req: [G('a750gl')] },
  'psu-msi-mag-a850gl': { req: [G('a850gl')] },
  'psu-msi-mpg-a850g': { req: [G('a850g')], ban: ['a850gl'] },
  'psu-seasonic-focus-gx750-atx30': { req: [G('gx750')] },
  'psu-seasonic-focus-gx850-atx30': { req: [G('gx850')] },
  'psu-seasonic-vertex-gx1000': { req: [G('vertex'), G('gx1000', '1000w')] },
  'psu-tt-gf3-750': { req: [G('gf3'), G('750w')], ban: ['850w'] },
  'psu-tt-gf3-850': { req: [G('gf3'), G('850w')], ban: ['750w'] },
  // 机箱
  'case-asus-prime-ap201': { req: [G('ap201')] },
  'case-bequiet-pure-base-500dx': { req: [G('500dx')] },
  'case-bequiet-shadow-base-800-fx': { req: [G('shadowbase800'), G('fx')] },
  'case-coolermaster-nr200p': { req: [G('nr200p')], ban: ['max'] },
  'case-coolermaster-td500-mesh-v2': { req: [G('td500'), G('mesh')] },
  'case-corsair-4000d-airflow': { req: [G('4000d'), G('airflow')], ban: ['5000d', 'rgb'] },
  'case-corsair-5000d-airflow': { req: [G('5000d'), G('airflow')], ban: ['5000t'] },
  'case-fractal-define-7': { req: [G('define7')], ban: ['xl', 'compact', 'define7c'] },
  'case-fractal-meshify-2-compact': { req: [G('meshify2'), G('compact')], ban: ['xl'] },
  'case-fractal-north': { req: [G('fractal', '分形'), G('north')] },
  'case-fractal-pop-air': { req: [G('fractal', '分形'), G('popair')], ban: ['rgb'] },
  'case-fractal-terra': { req: [G('fractal', '分形'), G('terra')] },
  'case-fractal-torrent': { req: [G('fractal', '分形'), G('torrent')], ban: ['compact', 'nano'] },
  'case-lianli-a3-matx': { req: [G('联力', 'lianli'), G('a3matx')] },
  'case-lianli-lancool-216': { req: [G('lancool216')] },
  'case-lianli-o11-dynamic-evo': { req: [G('o11'), G('evo')], ban: ['mini', 'xl'] },
  'case-montech-air-903-max': { req: [G('montech', '玩嘉'), G('903max')] },
  'case-nzxt-h5-flow-2024': { req: [G('nzxt', '恩杰'), G('h5flow')], ban: ['h510'] },
  'case-nzxt-h6-flow-2023': { req: [G('nzxt', '恩杰'), G('h6flow')] },
  'case-nzxt-h7-flow-2024': { req: [G('nzxt', '恩杰'), G('h7flow')], ban: ['rgb'] },
  // 散热器
  'cooler-arctic-lf2-240': { req: [G('liquidfreezerii', 'lf2'), G('240')], ban: ['iii'] },
  'cooler-arctic-lf3-240': { req: [G('liquidfreezeriii', 'lf3'), G('240')], ban: ['360'] },
  'cooler-arctic-lf3-360': { req: [G('liquidfreezeriii', 'lf3'), G('360')], ban: ['240'] },
  'cooler-bequiet-dark-rock-pro-4': { req: [G('darkrockpro4')] },
  'cooler-bequiet-dark-rock-pro-5': { req: [G('darkrockpro5')] },
  'cooler-coolermaster-hyper212-black': { req: [G('hyper212'), G('black')], ban: ['rgb'] },
  'cooler-corsair-h100i-elite-capellix-xt': { req: [G('h100i'), G('capellix'), G('xt')] },
  'cooler-corsair-h150i-elite-capellix-xt': { req: [G('h150i'), G('capellix'), G('xt')] },
  'cooler-deepcool-ag400': { req: [G('ag400')], ban: ['ag400pro', 'ag400max', 'ag400g2'] },
  'cooler-deepcool-ak620': { req: [G('ak620')], ban: ['digital', '数显', 'g2'] },
  'cooler-deepcool-assassin-iv': { req: [G('assassiniv', '阿萨辛'), G('4', 'iv')], ban: ['iii'] },
  'cooler-deepcool-lt520': { req: [G('lt520')] },
  'cooler-msi-mag-coreliquid-e360': { req: [G('coreliquid'), G('e360')] },
  'cooler-noctua-nh-d15': { req: [G('nhd15')], ban: ['d15s', 'chromax', 'g2', 'chbk'] },
  'cooler-noctua-nh-u12s': { req: [G('nhu12s')], ban: ['redux', 'tr4', 'sp3'] },
  'cooler-nzxt-kraken-240': { req: [G('kraken240')], ban: ['rgb'] },
  'cooler-nzxt-kraken-x63': { req: [G('krakenx63')] },
  'cooler-thermalright-frozen-prism-240': { req: [G('frozenprism', '冰封棱镜'), G('240')] },
  'cooler-thermalright-pa120se': { req: [G('pa120se')] },
  'cooler-thermalright-ps120se': { req: [G('ps120se', 'phantomspirit120se')] },
};

// ---------- 加载输入 ----------
const results = JSON.parse(fs.readFileSync(RESULTS, 'utf8'));
const oldRows = {};
for (const l of fs.readFileSync(OLDCSV, 'utf8').trim().split(/\r?\n/).slice(1)) {
  const [sku, price, source, at] = l.split(',');
  if (sku) oldRows[sku] = { price: parseFloat(price), source, at };
}

function parseSales(v) {
  const s = String(v || '').replace(/[+＋\s]/g, '');
  if (!s) return 0;
  if (s.includes('万')) return Math.round(parseFloat(s) * 10000) || 0;
  const n = parseFloat(s);
  return Number.isFinite(n) ? n : 0;
}

function sellerScore(shop) {
  const s = squash(shop);
  if (s.includes('京东自营') || s.includes('自营')) return 4;
  if (s.includes('官方旗舰店')) return 3;
  if (s.includes('旗舰店')) return 2;
  if (s.includes('专卖店')) return 1.5;
  return 0;
}

function classify(r) {
  const st = squash(r.title);
  const shop = squash(r.shopName);
  const price = parseFloat(r.actualPrice);
  return { st, shop, price };
}

// 前缀去重后统计命中的同品类型号数（b650 命中于 b650m 时只计一次）
function famHits(cat, st) {
  const toks = (FAM[cat] || []).filter((t) => st.includes(t));
  return toks.filter((t) => !toks.some((o) => o !== t && o.startsWith(t) && o.length > t.length));
}

const out = {};
const lines = [];
for (const sku of Object.keys(RULES)) {
  const entry = results[sku];
  const rule = RULES[sku];
  const cat = sku.split('-')[0];
  const old = oldRows[sku];
  const mult = cat === 'mem' ? 6 : cat === 'gpu' ? 4 : 3;
  const lo = old ? old.price * 0.3 : null;
  const hi = old ? old.price * mult : null;

  const cands = [];
  let nRows = 0, nJunk = 0, nOut = 0, nNoMatch = 0;
  for (const r of (entry && entry.rows) || []) {
    nRows++;
    const { st, shop, price } = classify(r);
    if (!Number.isFinite(price) || price <= 0) { nJunk++; continue; }
    const junkList = [...JUNK, ...(cat === 'case' ? [] : JUNK_CASE_OK), ...(cat === 'cpu' ? ['主板'] : [])];
    const junk = junkList.some((j) => st.includes(squash(j)));
    if (junk) { nJunk++; continue; }
    if ((lo && price < lo) || (hi && price > hi)) { nOut++; continue; }
    const okReq = (rule.req || []).every((grp) => grp.some((alt) => st.includes(squash(alt))));
    if (!okReq) { nNoMatch++; continue; }
    if ((rule.ban || []).some((b) => st.includes(squash(b)))) { nNoMatch++; continue; }

    let score = sellerScore(r.shopName) + Math.min(2, (Math.log10(1 + parseSales(r.monthSales)) / 5) * 2);
    const flags = [];
    for (const [tok, [flag, pen]] of Object.entries(SOFT)) {
      if (st.includes(squash(tok))) { flags.push(flag); score += pen; }
    }
    if (cat === 'cpu' && st.includes('盒装')) score += 0.5;
    const hits = famHits(cat, st);
    if (hits.length >= 2) { flags.push('multi_model_listing'); score -= 5; }
    if (old) {
      if (price < old.price * 0.7) { flags.push('suspicious_low'); score -= 3; }
      else if (price < old.price * 0.85) flags.push('price_below_baseline');
      if (price > old.price * 2) flags.push('above_baseline_2x');
    } else flags.push('no_baseline');
    const vrams = ['8g', '12g', '16g', '20g', '24g', '48g'].filter((v) => st.includes(v));
    if (rule.vram && vrams.length !== 1) flags.push('vram_check');
    cands.push({
      title: r.title, price, orig: parseFloat(r.originalPrice) || null, coupon: parseFloat(r.couponPrice) || 0,
      platform: PLATFORM[r.source] || r.source, shop: r.shopName, goodsId: r.goodsId, monthSales: parseSales(r.monthSales),
      score: Math.round(score * 100) / 100, flags,
    });
  }
  cands.sort((a, b) => b.score - a.score);
  const chosen = cands[0] || null;
  const delta = chosen && old ? Math.round(((chosen.price / old.price) - 1) * 100) : null;
  out[sku] = { keyword: entry ? entry.keyword : '', old: old ? old.price : null, nRows, nJunk, nOut, nNoMatch, cands: cands.slice(0, 5), chosen, delta };
  const ch = chosen ? `${chosen.price.toFixed(0)}元 ${delta > 0 ? '+' : ''}${delta}% ${chosen.platform} ${chosen.flags.join(',') || '-'} ${chosen.title.slice(0, 42)}` : 'NO_MATCH';
  lines.push(`${sku} old=${old ? old.price.toFixed(0) : '-'} => ${ch} (rows=${nRows} junk=${nJunk} out=${nOut} nomatch=${nNoMatch})`);
}

fs.writeFileSync(OUT, JSON.stringify(out, null, 1));
console.log(lines.join('\n'));
const nMatch = Object.values(out).filter((v) => v.chosen).length;
console.log(`MATCHED: ${nMatch}/${Object.keys(RULES).length} -> ${OUT}`);
