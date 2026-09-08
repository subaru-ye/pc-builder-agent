const fs = require('fs');
const path = require('path');
const { DATA } = require('./paths.cjs');

const ROOT = path.join(DATA, '..');
const results = JSON.parse(fs.readFileSync(path.join(DATA, 'maishou_full_results_20260908.json'), 'utf8'));
const match = JSON.parse(fs.readFileSync(path.join(DATA, 'maishou_match_candidates_20260908.json'), 'utf8'));
const PLATFORM = { 1: 'tb', 2: 'jd', 3: 'pdd', 4: 'sn', 5: 'vip', 7: 'dy', 8: 'ks' };

const lines = [];
const skus = Object.keys(match);
// A. 有候选的 SKU：chosen + top5 全标题
lines.push('=== A. AUTO-MATCHED SKUs (chosen + top candidates) ===');
for (const sku of skus) {
  const m = match[sku];
  if (!m.chosen) continue;
  lines.push(`\n## ${sku}  old=${m.old}  chosen=${m.chosen.price} (${m.delta}%)  flags=[${m.chosen.flags.join(',')}]  ${m.chosen.platform}  ${m.chosen.shop}`);
  lines.push(`   CH: ${m.chosen.title}`);
  for (const c of m.cands.slice(1)) {
    lines.push(`   ${c.score} ${c.price} ${c.platform} [${c.flags.join(',') || '-'}] ${c.title}`);
  }
}
// B. 无匹配 SKU：原始行（过滤疑似垃圾前，先展示全部，标疑）
lines.push('\n=== B. NO_MATCH SKUs (raw rows) ===');
for (const sku of skus) {
  const m = match[sku];
  if (m.chosen) continue;
  const entry = results[sku] || { rows: [] };
  lines.push(`\n## ${sku}  old=${m.old}  kw="${entry.keyword}"  rows=${entry.rows.length}`);
  const sorted = [...entry.rows]
    .filter((r) => Number.isFinite(parseFloat(r.actualPrice)) && parseFloat(r.actualPrice) > 0)
    .sort((a, b) => parseFloat(a.actualPrice) - parseFloat(b.actualPrice));
  for (const r of sorted.slice(0, 12)) {
    lines.push(`   ${parseFloat(r.actualPrice).toFixed(0)} ${PLATFORM[r.source] || r.source} ${String(r.shopName || '').slice(0, 18)} | ${r.title}`);
  }
}
fs.writeFileSync(path.join(DATA, 'maishou_review_20260908.txt'), lines.join('\n'));
console.log('written', lines.length, 'lines');
