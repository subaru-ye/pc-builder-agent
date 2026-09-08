const fs = require('fs');
const path = require('path');
const { ROOT, DATA, OUTPUT } = require('./paths.cjs');

const ORIG = path.join(DATA, 'maishou_full_results_20260908.json');
const SWEEP3 = path.join(DATA, 'maishou_sweep3_results_20260908.json');
const OUT = path.join(DATA, 'maishou_merged_20260908.json');

const orig = JSON.parse(fs.readFileSync(ORIG, 'utf8'));
const s3 = JSON.parse(fs.readFileSync(SWEEP3, 'utf8'));

const merged = JSON.parse(JSON.stringify(orig));
let added = 0, skusTouched = 0;
for (const [sku, e3] of Object.entries(s3)) {
  if (!merged[sku]) merged[sku] = { keyword: e3.keyword, rows: [] };
  const seen = new Set(merged[sku].rows.map((r) => r.goodsId));
  for (const r of e3.rows || []) {
    if (seen.has(r.goodsId)) continue;
    seen.add(r.goodsId);
    merged[sku].rows.push(r);
    added++;
  }
  if (e3.rows && e3.rows.length) skusTouched++;
}
fs.writeFileSync(OUT, JSON.stringify(merged, null, 1));
console.log(`merged: ${skusTouched} skus got new rows, +${added} deduped rows -> ${OUT}`);
for (const [sku, e3] of Object.entries(s3)) {
  console.log(`${sku}: kw="${e3.keyword}" new=${(e3.rows || []).length}`);
}
