"""Migrate frozen v1.5 Screening inputs without changing historical evidence.

The paired offline suite supplies explicit reducer oracles. The live suite has
identical inputs/grades and no oracle. Neither grade equates old default `any`
with a user preference or requires the retired fixed-field clarification path.
"""
import argparse
import copy
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
UNKNOWN = {'status': 'unknown'}
REMOVED = {'status': 'removed'}


def owned(model):
    return [{'category': 'cpu', 'model': model, 'quantity': 1}]


def updates():
    # Per-turn semantic expectations reviewed against original user wording.
    # These are eval answers, never code paths used by the product.
    gaming = {'use_case.type': 'gaming'}
    office = {'use_case.type': 'general', 'use_case.resolution': UNKNOWN}
    new_cpu = {'existing_parts': ['cpu'], 'owned_parts': owned('AMD Ryzen 5 7600')}
    return {
        'L3-101': [{**gaming, 'budget_cny': 8000, 'use_case.resolution': '2K', 'noise_pref': 'silent'}],
        'L3-102': [{'budget_cny': UNKNOWN, 'use_case.type': UNKNOWN, 'use_case.resolution': UNKNOWN}],
        'L3-103': [{**gaming, 'budget_cny': 8000, 'use_case.resolution': UNKNOWN}],
        'L3-104': [{**gaming, 'budget_cny': 8000, 'use_case.resolution': '2K'}],
        'L3-105': [{**gaming, 'budget_cny': 8000, 'use_case.resolution': '2K', 'brand_pref.cpu': 'amd', 'brand_pref.gpu': 'amd'}],
        'L4-211': [{**office, 'budget_cny': 6000, 'budget_basis': 'new_purchase', 'existing_parts': ['cpu', 'memory'], 'owned_parts': UNKNOWN}],
        'L4-212': [{**office, **new_cpu, 'budget_cny': 6000, 'budget_basis': UNKNOWN}],
        'L4-213': [{**gaming, 'budget_cny': 8000, 'budget_basis': 'new_purchase', 'use_case.resolution': '2K', 'existing_parts': ['gpu'], 'owned_parts': UNKNOWN}],
        'L4-214': [{**office, **new_cpu, 'budget_cny': UNKNOWN, 'budget_basis': 'new_purchase'}],
        'L5-301': [{**gaming, 'budget_cny': 8000, 'use_case.resolution': UNKNOWN}, {'budget_cny': 6000}, {'use_case.resolution': '2K'}],
        'L5-302': [{**gaming, 'budget_cny': 8000, 'noise_pref': 'silent', 'use_case.resolution': UNKNOWN}, {'use_case.resolution': '2K'}],
        'L5-303': [{**gaming, 'budget_cny': UNKNOWN, 'use_case.resolution': '2K'}, {'budget_cny': 8000}],
        'L5-304': [{**office, 'budget_cny': 6000, 'existing_parts': ['cpu'], 'owned_parts': UNKNOWN, 'budget_basis': UNKNOWN}, {'owned_parts': owned('AMD Ryzen 5 7600')}, {'budget_basis': 'new_purchase'}],
        'L5-305': [{**office, **new_cpu, 'budget_cny': 6000, 'budget_basis': 'new_purchase'}, {'budget_basis': 'full_build'}],
        'L5-306': [{**office, 'budget_cny': UNKNOWN}, {'budget_cny': 8500}],
        'L5-307': [{**gaming, 'budget_cny': 10000, 'use_case.resolution': '2K', 'brand_pref.cpu': 'amd', 'brand_pref.gpu': 'any'}, {'brand_pref.cpu': 'intel'}],
        'L5-308': [{**office, **new_cpu, 'budget_cny': 6000, 'budget_basis': 'new_purchase'}, {'owned_parts': owned('Intel Core i5-12400F')}],
        'L5-309': [{**office, 'budget_cny': 8000}, {**gaming, 'use_case.resolution': UNKNOWN}, {'use_case.resolution': '2K', 'budget_cny': 8000}],
        'L5-310': [{**gaming, 'budget_cny': 8000, 'use_case.resolution': '2K'}, {'budget_cny': REMOVED}],
    }


def canonical_hash(value):
    text = json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(',', ':'))
    for a, b in [('<', '\\u003c'), ('>', '\\u003e'), ('&', '\\u0026')]:
        text = text.replace(a, b)
    return hashlib.sha256(text.encode('utf-8')).hexdigest()


def build_case(case, deltas):
    turns = case.get('turns') or [{'input': case['input'], 'expect': case['expect']}]
    if len(turns) != len(deltas):
        raise ValueError('Turn mapping mismatch: ' + case['id'])
    fields = {key: copy.deepcopy(UNKNOWN) for key in ['brand_pref.cpu', 'brand_pref.gpu', 'budget_flex', 'noise_pref']}
    steps = []
    for index, (turn, delta) in enumerate(zip(turns, deltas)):
        operations = []
        for key, value in delta.items():
            if isinstance(value, dict) and 'status' in value:
                fields[key] = copy.deepcopy(value)
                if value['status'] == 'removed':
                    operations.append({'op': 'remove', 'field': key, 'quote': turn['input']})
                continue
            kind = 'fact' if key in {'use_case.type', 'use_case.resolution', 'owned_parts', 'existing_parts'} else 'constraint'
            strength = 'prefer' if key == 'noise_pref' or key.startswith('brand_pref.') else 'must'
            fields[key] = {'status': 'active', 'value': value}
            if key == 'noise_pref':
                fields[key]['strength'] = strength
            operations.append({'op': 'set', 'field': key, 'value': value, 'kind': kind,
                               'strength': strength, 'evidence': 'stated', 'quote': turn['input']})
        expect = {'versions': 0, 'builder_calls': 0, 'fields': copy.deepcopy(fields)}
        # Retain the old actionable-final expectation under the new confirmation
        # protocol, without making absent optional fields mandatory beforehand.
        if index == len(turns) - 1 and case['id'] not in {'L3-102', 'L4-207', 'L4-208', 'L4-211', 'L4-212', 'L4-213', 'L4-214', 'L5-310'}:
            expect['next_action'] = 'confirm'
        steps.append({'kind': 'message', 'text': turn['input'], 'expect': expect,
                      'screen_oracle': {'operations': operations, 'next_action': expect.get('next_action', 'collect'),
                                        'reply': '已记录本轮信息，可以继续核对需求。'}})
    steps.append({'kind': 'refresh', 'expect': {'versions': 0, 'builder_calls': 0, 'fields': copy.deepcopy(fields)}})
    return {'id': case['id'], 'title': case['title'], 'source': 'Frozen v1.5 case; unchanged user messages, explicitly migrated state assertions', 'steps': steps}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--out', required=True, type=Path)
    args = parser.parse_args()
    original = ROOT / 'internal/evalsuite/testdata'
    manifest_raw = (original / 'suites/v1.5.json').read_bytes()
    manifest = json.loads(manifest_raw)
    mapping = updates()
    cases, sources = [], {}
    for entry in manifest['cases']:
        case = json.loads((original / 'cases' / entry['file']).read_bytes())
        if canonical_hash(case) != entry['sha256']:
            raise ValueError('Frozen historical case changed: ' + entry['id'])
        if case.get('stage') == 'screening':
            cases.append(build_case(case, mapping[case['id']]))
            sources[case['id']] = entry['sha256']
    if set(sources) != set(mapping):
        raise ValueError('Screening migration must cover every historical Screening case')
    catalog_path = ROOT / 'internal/planningeval/testdata/live-smoke-20260915/suite.json'
    catalog_raw = catalog_path.read_bytes()
    catalog_pin = json.loads(catalog_path.with_name('provenance.json').read_bytes())
    if hashlib.sha256(catalog_raw).hexdigest() != catalog_pin['suite_sha256']:
        raise ValueError('Frozen catalog suite changed')
    offline = {'version': 'legacy-screening-v1.5-planning-20260915',
               'provenance': '19 historical Screening scenarios, unchanged user text; new explicit state/dispatch grades. Offline model decisions are authored oracles.',
               'catalog': json.loads(catalog_raw)['catalog'], 'pages': {}, 'cases': cases}
    live = copy.deepcopy(offline)
    live['live'] = True
    live['provenance'] = 'Same 19 historical Screening inputs and migrated grades; live model decisions, no oracle. No planning confirmation or external calls.'
    for case in live['cases']:
        for step in case['steps']:
            step.pop('screen_oracle', None)
    args.out.mkdir(parents=True, exist_ok=False)
    hashes = {}
    for name, suite in [('offline', offline), ('live', live)]:
        raw = (json.dumps(suite, ensure_ascii=False, indent=2) + '\n').encode('utf-8')
        child = args.out / name
        child.mkdir()
        (child / 'suite.json').write_bytes(raw)
        hashes[name] = hashlib.sha256(raw).hexdigest()
    calls = sum(step['kind'] == 'message' for case in cases for step in case['steps'])
    provenance = {'original_suite_sha256': hashlib.sha256(manifest_raw).hexdigest(),
                  'original_cases': sources, 'generated_suite_sha256': hashes,
                  'catalog_source_sha256': hashlib.sha256(catalog_raw).hexdigest(),
                  'generator_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                  'screening_requests_per_repeat': calls,
                  'comparison_limits': ['Historical default any becomes unknown when not stated',
                                        'Fixed mandatory clarification outcomes retired; actionable final turns require confirm',
                                        'No Builder execution: this subset measures state and dispatch, not build quality',
                                        'Manual review must separately check optional questions and correct interpretation; fixed-field grades are not a semantic judge',
                                        'Old single-Spec outputs cannot prove historical provenance/removal statuses; compare only representable values separately'],
                  'offline_oracles': 'Authored per-turn operations; never used in live model requests'}
    (args.out / 'provenance.json').write_bytes((json.dumps(provenance, ensure_ascii=False, indent=2) + '\n').encode('utf-8'))
    for name, digest in hashes.items():
        child_provenance = {**provenance, 'suite_sha256': digest}
        (args.out / name / 'provenance.json').write_bytes((json.dumps(child_provenance, ensure_ascii=False, indent=2) + '\n').encode('utf-8'))
    print(f'Migrated {len(cases)} historical Screening scenarios, {calls} message turns, paired offline/live suites')


if __name__ == '__main__':
    main()
