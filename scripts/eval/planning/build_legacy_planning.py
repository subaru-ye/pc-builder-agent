"""Freeze all 31 v1.5 Builder cases for the current product planning path.

Original typed inputs enter through the real requirement edit/confirmation API;
this subset does not claim to test natural-language Screening. Historical base
selections are seeded before edits, never replaced with a newly generated base.
No provider calls, historical rewrites or generated model answers occur here.
"""
import argparse
import copy
import hashlib
import json
from decimal import Decimal
from pathlib import Path

from build_legacy_screening import ROOT, canonical_hash


def migrated_operations(case):
    operations = []
    requirement = case['requirement']
    for name, value in requirement.items():
        if name == 'schema_version':
            continue
        fields = {name + '.' + k: v for k, v in value.items()} if name in {'use_case', 'brand_pref'} else {name: value}
        for key, field_value in fields.items():
            # Explicit original fields only; no populated decoder defaults.
            kind = 'fact' if key.startswith('use_case.') or key in {'existing_parts', 'owned_parts'} else 'constraint'
            strength = 'prefer' if key in {'noise_pref', 'notes', 'priority'} else 'must'
            operations.append({'op': 'set', 'field': key, 'value': field_value, 'kind': kind, 'strength': strength})
    if 'change' in case:
        # Preserve the complete original instruction, including target hints,
        # budget deltas and locks. Context is an execution request, not a new
        # hardware condition requiring a fabricated evidence citation.
        operations.append({'op': 'set', 'field': 'free.historical_change', 'kind': 'context',
                           'value': json.dumps(case['change'], ensure_ascii=False, separators=(',', ':'))})
    if case.get('locked'):
        operations.append({'op': 'set', 'field': 'free.locked_parts', 'kind': 'constraint', 'strength': 'must',
                           'value': '必须保留原方案配件：' + '、'.join(case['locked'])})
    return operations


def expectations(case, operations, catalog):
    base = int('base_selection' in case)
    fields = {op['field']: {'value': op['value'], 'status': 'active', 'kind': op['kind']} for op in operations}
    requirement = case['requirement']
    expect = {'versions': base + 1, 'outcome': 'ready', 'validation': 'pass', 'missing_prices': 0,
              'fields': fields, 'require_tools': ['search_local', 'evaluate'],
              'budget_ceiling_cny': str(Decimal(requirement['budget_cny']) * (1 + Decimal(str(requirement.get('budget_flex', 0)))))}
    brands = requirement.get('brand_pref', {})
    if brands.get('cpu'):
        expect['selected_brands'] = {'cpu': brands['cpu']}
    gpu_brand = brands.get('gpu')
    if case.get('change', {}).get('intent') == 'swap_part':
        gpu_brand = 'amd'  # All four original swap requests explicitly name AMD.
    if gpu_brand:
        token = {'amd': 'Radeon', 'nvidia': 'GeForce'}[gpu_brand]
        expect['selected_options'] = {'gpu': [p['id'] for p in catalog['candidates'] if p['category'] == 'gpu' and token in p['model']]}
    if requirement.get('size_pref') == 'itx':
        expect.setdefault('selected_options', {})['case'] = ['case-fractal-terra', 'case-coolermaster-nr200p']
        expect['selected_specs'] = {'motherboard': {'form_factor': 'itx'}}
    if case.get('locked'):
        expect['selected_parts'] = {c: case['base_selection']['parts'][c] for c in case['locked']}
    if case['id'] == 'L4-206':
        expect['purchase_budget'] = True
        expect.pop('missing_prices')  # The procurement sum must be complete; owned items need not be priced.
        expect['selected_parts'] = {'cpu': 'cpu-r5-7600', 'memory': 'mem-adata-lancer-32-5200'}
    pending = {
        'L4-201': ['型号'], 'L4-202': ['预算口径', '新增', '整机'],
        'L4-203': ['预算'], 'L4-204': ['XYZ-000', '型号'],
        'L4-205': ['DDR4', 'ddr4', 'AM5', '兼容'],
        'L4-209': ['型号'], 'L4-210': ['型号'],
    }
    if case['id'] in pending:
        expect = {'versions': 0, 'outcome_one_of': ['clarify', 'proposal'], 'fields': fields,
                  'require_tools': ['search_local'], 'issues_any': pending[case['id']]}
        if case['id'] in {'L4-203', 'L4-205'}:
            expect['require_tools'].append('evaluate')
    # L4-215 remains a strict budget-delivery trial. Any non-delivery is reported
    # for independent evidence review, not automatically accepted as adaptive.
    return expect


def build_case(case, catalog):
    operations = migrated_operations(case)
    expected = expectations(case, operations, catalog)
    fields = copy.deepcopy(expected['fields'])
    initial_versions = int('base_selection' in case)
    result = {'id': case['id'], 'title': case['title'],
              'source': 'Frozen v1.5 typed Builder input via product edit/confirm; original change and base retained',
              'steps': [{'kind': 'edit', 'edit': operations, 'expect': {'versions': initial_versions, 'builder_calls': 0, 'fields': fields}},
                        {'kind': 'confirm', 'expect': expected},
                        {'kind': 'refresh', 'expect': {'versions': expected['versions'], 'builder_calls': 0, 'fields': fields}}]}
    if 'base_selection' in case:
        prior = copy.deepcopy(case['requirement'])
        change = case['change']
        if change['intent'] == 'adjust_budget':
            prior['budget_cny'] -= change['budget_delta_cny']
        elif change['intent'] == 'change_constraint':
            for key in change['constraint_patch']:
                prior.pop(key, None)
        result['previous_build'] = {'selection': case['base_selection'], 'requirement': prior,
                                    'source': 'Original base_selection; prior budget derived by reversing explicit delta, changed constraints omitted; facts repriced against frozen evaluation catalog'}
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--ids', nargs='+', help='Explicit bounded subset; default all 31')
    args = parser.parse_args()
    historical = ROOT / 'internal/evalsuite/testdata'
    manifest_raw = (historical / 'suites/v1.5.json').read_bytes()
    manifest = json.loads(manifest_raw)
    catalog_path = ROOT / 'internal/planningeval/testdata/live-smoke-20260915/suite.json'
    catalog_raw = catalog_path.read_bytes()
    if hashlib.sha256(catalog_raw).hexdigest() != json.loads(catalog_path.with_name('provenance.json').read_bytes())['suite_sha256']:
        raise ValueError('Frozen catalog drift')
    catalog = json.loads(catalog_raw)['catalog']
    cases, originals = [], {}
    for entry in manifest['cases']:
        case = json.loads((historical / 'cases' / entry['file']).read_bytes())
        if canonical_hash(case) != entry['sha256']:
            raise ValueError('Historical input drift: ' + entry['id'])
        if case.get('stage') == 'screening':
            continue
        originals[case['id']] = entry['sha256']
        cases.append(build_case(case, catalog))
    if len(cases) != 31:
        raise ValueError('Expected exactly 31 historical Builder cases')
    if args.ids:
        if len(set(args.ids)) != len(args.ids) or not set(args.ids) <= originals.keys():
            raise ValueError('Unknown/duplicate selected historical case')
        cases = [c for c in cases if c['id'] in args.ids]
        originals = {c['id']: originals[c['id']] for c in cases}
    suite = {'version': 'legacy-builder-v1.5-planning-20260915', 'live': True,
             'provenance': '31-case typed input migration; real Builder, product state/persistence; no Screening or external network',
             'catalog': catalog, 'pages': {}, 'cases': cases}
    raw = (json.dumps(suite, ensure_ascii=False, indent=2) + '\n').encode('utf-8')
    provenance = {'suite_sha256': hashlib.sha256(raw).hexdigest(), 'original_cases': originals,
                  'original_suite_sha256': hashlib.sha256(manifest_raw).hexdigest(),
                  'catalog_source_sha256': hashlib.sha256(catalog_raw).hexdigest(),
                  'generator_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                  'scope': 'Product edit, requirement confirmation, actual Builder tools, persistence and refresh; no natural-language Screening',
                  'comparison_limits': [
                      'Historical explicit typed facts only; omitted defaults remain unknown',
                      'Budget/brand/size/locks retained; noise, appearance and priority are soft preferences, semantic fulfillment needs separate review',
                      'FPS/use-case performance are goals, not measured guarantees; compatibility passing is insufficient to prove them',
                      'Original modification bases retained; their quote and validation are recomputed for this catalog and journaled, not counted as new delivery',
                      'Known-model/missing-budget-basis cases accept evidenced clarification or proposal, not retired deterministic rejection',
                      'L4-215 budget-adaptive case uses strict delivery as automated trial; independently justified non-delivery requires separate review',
                      'GPU vendor grade uses frozen Radeon/GeForce model identities, not board manufacturer; ITX requires an ITX board plus Terra/NR200P case',
                      'No exact FPS or performance-nonregression claim without evidence review; automated pass alone is not full historical parity'],
                  'max_builder_requests_per_case': 8, 'external_requests_limit': 0, 'embedding_requests_limit': 0}
    args.out.mkdir(parents=True, exist_ok=False)
    (args.out / 'suite.json').write_bytes(raw)
    (args.out / 'provenance.json').write_bytes((json.dumps(provenance, ensure_ascii=False, indent=2) + '\n').encode('utf-8'))
    print(f'Frozen {len(cases)} Builder cases; no model or database calls')


if __name__ == '__main__':
    main()
