"""Two bounded detail lookups using the installed maishou skill and old driver.

Preserves the original collection checkpoint and raw files. The new checkpoint
carries forward its budget counters, so this is not a fresh 120-call allowance.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path

ROOT = Path(__file__).resolve().parents[4]
CHOICES = {'case-lianli-lancool-207': 'f86260e8a97e7a86',
           'mem-kingbank-xingren-48-6000': '22dcdb8990630df3'}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--out', required=True, type=Path)
    args = parser.parse_args()
    original = ROOT / 'var/data/collections/2026-09-13-maishou-expansion-01/checkpoint.json'
    raw_checkpoint = original.read_bytes()
    checkpoint = json.loads(raw_checkpoint)
    if checkpoint['detail_count'] + len(CHOICES) > 120:
        raise ValueError('original collection detail budget would be exceeded')
    source = ROOT / 'scripts/data/catalog-candidates/2026-09-13/offers.jsonl'
    raw = source.read_bytes()
    if hashlib.sha256(raw).hexdigest() != '1152debccc4a319932d9a9971493531bb5d5b89b9f9e895fb028f4160567b0b6':
        raise ValueError('reviewed offer file changed')
    rows = {r['row_key']: r for r in map(json.loads, raw.decode('utf-8').splitlines())}
    plan = []
    for identity, row_key in CHOICES.items():
        row = rows[row_key]
        if row['model_id'] != identity or row.get('buy_url') or not row['is_primary']:
            raise ValueError('reviewed detail target changed')
        plan.append({'detail_id': identity, 'row_key': row_key,
                     'goodsId': row['goodsId'], 'source': row['platform_src']})
    args.out.mkdir(parents=True, exist_ok=False)
    os.environ['COLLECTION_DATA_DIR'] = str(args.out.resolve())
    module_path = ROOT / 'scripts/data/collection-tools/2026-09-13/expand_driver.py'
    spec = importlib.util.spec_from_file_location('reviewed_detail_driver', module_path)
    driver = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(driver)
    start_count = checkpoint['detail_count']
    driver.BUDGET_DETAIL = start_count + len(plan)
    checkpoint.update(batch='2026-09-15-reviewed-details', details={}, queries={}, history=[],
                      parent_checkpoint_sha256=hashlib.sha256(raw_checkpoint).hexdigest(),
                      parent_detail_count=start_count, retries=0)
    driver.save_checkpoint(checkpoint)
    (args.out / 'plan.json').write_bytes((json.dumps(plan, ensure_ascii=False, indent=2) + '\n').encode('utf-8'))
    for entry in plan:
        driver.do_detail(checkpoint, entry)
        if checkpoint['details'][entry['detail_id']]['status'] != 'ok':
            raise SystemExit('detail failed; preserved output and budget, no retry')
    if original.read_bytes() != raw_checkpoint:
        raise ValueError('original collection changed concurrently; re-audit combined usage')
    print('New detail calls:', checkpoint['detail_count'] - start_count, '; original checkpoint unchanged')


if __name__ == '__main__': main()
