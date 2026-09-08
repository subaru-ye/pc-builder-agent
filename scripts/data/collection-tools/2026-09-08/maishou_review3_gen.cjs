const fs = require('fs');
const path = require('path');
const { DATA } = require('./paths.cjs');

const results = JSON.parse(fs.readFileSync(path.join(DATA, 'maishou_merged_20260908.json'), 'utf8'));
const match = JSON.parse(fs.readFileSync(path.join(DATA, 'maishou_match_candidates3_20260908.json'), 'utf8'));
const PLATFORM = { 1: 'tb', 2: 'jd', 3: 'pdd', 4: 'sn', 5: 'vip', 7: 'dy', 8: 'ks' };

const CARRY = ['cpu-i3-12100f','cpu-i5-12600k','cpu-r9-7900','cpu-ultra5-245k','gpu-asus-4060ti-dual-evo','gpu-gb-5060-windforce','gpu-gb-5060ti-windforce-16g','gpu-gb-5070-windforce-sff','gpu-gb-5080-gaming','gpu-intel-b580-le','gpu-msi-3060-ventus2x','gpu-msi-4060-ventus2x-black','gpu-msi-4070s-ventus2x','gpu-msi-5070ti-ventus3x','gpu-sapphire-6600-pulse','gpu-sapphire-7600-pulse','gpu-sapphire-7700xt-pulse','gpu-sapphire-7900xt-pulse','gpu-sapphire-9060xt-pulse-16g','gpu-sapphire-9070xt-pulse','mb-gb-b550-aorus-elite-v2','mb-gb-b650-aorus-elite-ax','mb-gb-b650i-aorus-ultra','mb-msi-b550-tomahawk','mb-gb-b760-gaming-x-ax','mb-msi-b860-tomahawk-wifi','mb-asrock-b650m-hdv-m2','mb-msi-b650-tomahawk-wifi','mem-corsair-lpx-32-3600','mem-corsair-veng-32-6000','mem-corsair-veng-rgb-32-6000','mem-gskill-s5-32-6000','mem-gskill-z5-rgb-32-6400','mem-gskill-z5neo-rgb-32-6000','mem-adata-lancer-32-5200','mem-team-delta-32-3200-d4','mem-team-delta-32-6000-white','mem-gskill-ripjawsv-32-3200','ssd-crucial-p3plus-1tb','ssd-crucial-p5plus-1tb','ssd-intel-670p-1tb','ssd-samsung-870evo-1tb','ssd-samsung-870qvo-2tb','ssd-samsung-970evoplus-1tb','ssd-samsung-980pro-1tb','ssd-wd-sn580-1tb','ssd-wd-sn770-1tb','ssd-adata-legend800-1tb','ssd-adata-s70blade-1tb','psu-bq-pp12m-750','psu-bq-sp12-750','psu-bq-sp12-1000','psu-corsair-cx650m','psu-msi-mpg-a850g','psu-seasonic-focus-gx750-atx30','case-bequiet-pure-base-500dx','case-bequiet-shadow-base-800-fx','case-corsair-4000d-airflow','case-corsair-5000d-airflow','case-fractal-define-7','case-fractal-meshify-2-compact','case-fractal-pop-air','case-lianli-lancool-216','case-lianli-o11-dynamic-evo','case-montech-air-903-max','case-nzxt-h5-flow-2024','case-nzxt-h6-flow-2023','case-nzxt-h7-flow-2024','cooler-arctic-lf2-240','cooler-bequiet-dark-rock-pro-4','cooler-corsair-h100i-elite-capellix-xt','cooler-corsair-h150i-elite-capellix-xt','cooler-deepcool-ag400','cooler-deepcool-lt520','cooler-msi-mag-coreliquid-e360','cooler-nzxt-kraken-240','cooler-nzxt-kraken-x63','cooler-noctua-nh-d15'];

const lines = [];
lines.push(`=== RESWEEP REVIEW (78 carried SKUs, merged rows) ${new Date().toISOString()} ===`);
for (const sku of CARRY) {
  const m = match[sku];
  if (!m) { lines.push(`\n## ${sku}  (no match entry)`); continue; }
  const entry = results[sku] || { rows: [] };
  lines.push(`\n## ${sku}  old=${m.old}  rows=${m.nRows} junk=${m.nJunk} out=${m.nOut} nomatch=${m.nNoMatch}  kw="${entry.keyword}"`);
  if (m.chosen) {
    lines.push(`   CH: ${m.chosen.price} (${m.delta}%) ${m.chosen.platform} [${m.chosen.flags.join(',') || '-'}] ${m.chosen.shop} | ${m.chosen.title}`);
    for (const c of m.cands.slice(1)) {
      lines.push(`   ${c.score} ${c.price} ${c.platform} [${c.flags.join(',') || '-'}] ${c.shop} | ${c.title}`);
    }
  } else {
    lines.push('   CH: NO_MATCH');
  }
  const sorted = [...entry.rows]
    .filter((r) => Number.isFinite(parseFloat(r.actualPrice)) && parseFloat(r.actualPrice) > 0)
    .sort((a, b) => parseFloat(a.actualPrice) - parseFloat(b.actualPrice));
  for (const r of sorted.slice(0, 14)) {
    lines.push(`   R ${parseFloat(r.actualPrice).toFixed(0)} ${PLATFORM[r.source] || r.source} ${String(r.shopName || '').slice(0, 18)} | ${r.title}`);
  }
}
fs.writeFileSync(path.join(DATA, 'maishou_review3_20260908.txt'), lines.join('\n'));
console.log('written', lines.length, 'lines');
