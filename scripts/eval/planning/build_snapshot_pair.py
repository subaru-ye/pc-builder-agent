"""Build reviewed data-difference oracles; reads frozen files, never the live DB.

The model decisions are identical on both sides. Expectations describe known
data changes, not expected model quality. Original fixture revisions stay intact.
"""
import argparse
import copy
import hashlib
import json
from decimal import Decimal
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
SAVED = ROOT / 'internal/planning/testdata/complete_proposal_recording.json'
MISSING = {'gpu-sapphire-6600-pulse', 'mem-gskill-ripjawsv-32-3200',
           'ssd-crucial-p3plus-1tb', 'case-montech-air-903-max'}
NEW_CPU = 'cpu-r9-9950x3d'


def load_pinned(path, expected):
    raw = path.read_bytes()
    if hashlib.sha256(raw).hexdigest() != expected:
        raise ValueError('frozen input hash mismatch: ' + str(path))
    return json.loads(raw)


def catalog(data, snapshot_id):
    snapshots = [row for row in data['snapshots'] if row['id'] == snapshot_id]
    if len(snapshots) != 1:
        raise ValueError('exactly one explicit price snapshot ID required')
    parts = [p for p in data['parts'] if p['active'] and p['catalog_state'] == 'active_core']
    if len({p['sku'] for p in parts}) != len(parts):
        raise ValueError('duplicate active part')
    rows = [p for p in data['prices'] if p['snapshot_id'] == snapshot_id]
    prices = {p['sku']: p for p in rows}
    if len(prices) != len(rows):
        raise ValueError('duplicate quote')
    candidates, metadata = [], {}
    for part in parts:
        sku = part['sku']
        price = prices.get(sku)
        if price is not None:
            number = Decimal(str(price['price_cny']))
            if not number.is_finite() or number <= 0:
                raise ValueError('invalid quote: ' + sku)
            metadata[sku] = {key: price[key] for key in
                             ('source', 'observed_at', 'price_type', 'availability_basis')}
        candidates.append({'id': sku, 'category': part['category'], 'brand': part['brand'],
                           'model': part['model'], 'specs': part['specs'],
                           'price_cny': format(number, 'f') if price is not None else None,
                           'external': False, 'evidence': []})
    ids = {p['id'] for p in candidates}
    evidence = [{'id': e['evidence_id'], 'candidate_id': e['sku'], 'field': e['field_path'],
                 'url': e['source_url'], 'title': e['sku'] + ' · ' + e['field_path'],
                 'text': e.get('evidence_excerpt') or '', 'captured_at': e['captured_at'],
                 'kind': 'local_catalog'}
                for e in data.get('evidence') or []
                if e['sku'] in ids and e['evidence_status'] == 'verified']
    return {'date': snapshots[0]['snapshot_date'], 'candidates': candidates,
            'evidence': evidence, 'price_metadata': metadata}


def text(value):
    return {'role': 'model', 'parts': [{'text': json.dumps(value, ensure_ascii=False)}]}


def tool(action, payload):
    return {'role': 'model', 'parts': [{'functionCall': {'name': 'planning_action',
            'id': 'paired-' + action, 'args': {'action': action,
            'payload': json.dumps(payload, ensure_ascii=False)}}}]}


def cases(saved, latest):
    draft = saved['draft']
    if isinstance(draft, str):
        draft = json.loads(draft)
    final = {k: saved[k] for k in ('outcome', 'reply', 'draft', 'issues', 'assessments', 'assumptions')}
    phrase = '预算7000，主要用于视频剪辑'
    first = {'kind': 'message', 'text': phrase, 'screen_oracle': {
        'next_action': 'confirm', 'reply': '已整理需求，可以开始选配。', 'operations': [
            {'op': 'set', 'field': 'budget_cny', 'value': 7000, 'strength': 'must', 'quote': phrase},
            {'op': 'set', 'field': 'use_case.type', 'value': 'productivity', 'kind': 'fact',
             'strength': 'must', 'quote': phrase}]}, 'expect': {'versions': 0, 'builder_calls': 0}}
    # The unchanged saved final answer is repeated if the server asks for repair.
    # This intentionally tests server truthfulness at the bound, not smart repair.
    outputs = [tool('search_local', {'category': 'cpu'}), tool('evaluate', {'draft': draft})] + [text(final)] * 6
    expected = {'versions': 0 if latest else 1, 'outcome': 'proposal' if latest else 'ready',
                'validation': 'pass', 'missing_prices': 4 if latest else 0,
                'require_tools': ['search_local', 'evaluate'], 'forbid_tools': ['search_web', 'read_page']}
    if latest:
        expected['issues_contain'] = ['价格未知']
    quote_case = {'id': 'D2-001', 'title': '相同保存方案在新旧完整目录中的实际报价与交付',
                  'source': 'complete_proposal_recording.json：最终模型输出原样复用；工具轨迹为人工 oracle',
                  'steps': [first, {'kind': 'confirm', 'builder_oracle': outputs, 'expect': expected},
                            {'kind': 'refresh', 'expect': {'versions': expected['versions'], 'builder_calls': 0}}]}
    search = {'id': 'D2-002', 'title': '新增9950X3D能否经本地检索返回模型',
              'source': '已审核新增CPU；人工检索协议，不预设模型会自主选择该CPU',
              'steps': [copy.deepcopy(first), {'kind': 'confirm', 'builder_oracle': [
                  tool('search_local', {'category': 'cpu', 'query': '9950X3D', 'limit': 24}),
                  text({'outcome': 'collect', 'reply': '已读取目录，可继续比较处理器方向。',
                        'issues': [], 'assessments': [], 'assumptions': []})], 'expect': {
                            'versions': 0, 'outcome': 'collect', 'builder_calls': 2,
                            'require_tools': ['search_local'], 'search_candidates': {NEW_CPU: latest}}}]}
    return [quote_case, search]


def validate_changes(old, new, saved):
    lines = saved['quote']['lines']
    wanted = {p['sku'] for p in lines}
    before = {p['id']: p for p in old['candidates']}
    after = {p['id']: p for p in new['candidates']}
    if not wanted <= before.keys() or not wanted <= after.keys():
        raise ValueError('saved build identity missing; review this scenario revision')
    if any(before[k]['price_cny'] is None for k in wanted):
        raise ValueError('old build no longer has complete quotes')
    if {k for k in wanted if after[k]['price_cny'] is None} != MISSING:
        raise ValueError('reviewed missing-price expectation drifted')
    if NEW_CPU in before or NEW_CPU not in after:
        raise ValueError('reviewed new-model expectation drifted')


def main():
    parser = argparse.ArgumentParser()
    for side in ('old', 'new'):
        parser.add_argument('--' + side, required=True, type=Path)
        parser.add_argument('--' + side + '-sha256', required=True)
        parser.add_argument('--' + side + '-id', required=True, type=int)
    parser.add_argument('--out', required=True, type=Path)
    args = parser.parse_args()
    saved_raw = SAVED.read_bytes()
    saved = json.loads(saved_raw)['result']
    catalogs = {side: catalog(load_pinned(getattr(args, side), getattr(args, side + '_sha256')),
                              getattr(args, side + '_id')) for side in ('old', 'new')}
    validate_changes(catalogs['old'], catalogs['new'], saved)
    args.out.mkdir(parents=True, exist_ok=False)
    for side in ('old', 'new'):
        target = args.out / side
        target.mkdir()
        suite = {'version': 'planning-data-pair-20260915-' + side,
                 'provenance': 'Full frozen active catalog; identical recorded/oracle decisions across data snapshots. Not live model quality.',
                 'catalog': catalogs[side], 'pages': {}, 'cases': cases(saved, side == 'new')}
        raw = (json.dumps(suite, ensure_ascii=False, indent=2) + '\n').encode('utf-8')
        (target / 'suite.json').write_bytes(raw)
        provenance = {'suite_sha256': hashlib.sha256(raw).hexdigest(),
                      'database_snapshot_sha256': getattr(args, side + '_sha256'),
                      'source_snapshot_id': getattr(args, side + '_id'),
                      'source_snapshot_file': str(getattr(args, side)),
                      'saved_result_sha256': hashlib.sha256(saved_raw).hexdigest(),
                      'generator_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                      'limitations': ['Scripted model decisions; no live performance conclusion.',
                                      'Embeddings, release/observation foreign keys and evidence ingestion lineage are not restored.',
                                      'Specs, quote values, quote observation metadata and verified evidence excerpts are retained.']}
        (target / 'provenance.json').write_bytes((json.dumps(provenance, ensure_ascii=False, indent=2) + '\n').encode('utf-8'))
    print('Built paired frozen suites; no database or provider calls')


if __name__ == '__main__':
    main()
