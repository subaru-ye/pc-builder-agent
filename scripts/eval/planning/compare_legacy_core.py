"""Read-only regrading of shared hardware/accounting facts, never old verdicts.

Each real batch remains a separate sample. This does not rate prose quality,
Screening, retired refusal paths, or claim that different catalogs isolate model
quality. Outputs are always written to a new directory.
"""
import argparse
import hashlib
import json
from collections import defaultdict
from decimal import Decimal
from pathlib import Path

from build_legacy_screening import ROOT, canonical_hash

RULES = {'SOCKET_MATCH', 'CHIPSET_SUPPORT', 'MEMORY_GENERATION', 'MEMORY_SPEED',
         'GPU_CLEARANCE', 'COOLER_CLEARANCE', 'PSU_HEADROOM', 'FORM_FACTOR_SUPPORT',
         'M2_SLOT_CAPACITY', 'GPU_POWER_CONNECTORS', 'DISPLAY_OUTPUT', 'COOLER_THERMAL_CAPACITY'}


def common_checks(case, selection, catalog, validation, quote, accepted):
    req = case['requirement']
    checks = {'accepted_result': accepted}
    rules = (validation or {}).get('checks', [])
    checks['compatibility'] = ((validation or {}).get('overall_status') == 'pass'
                               and {c['rule_id'] for c in rules} >= RULES
                               and all(c['outcome'] == 'pass' for c in rules))
    quote = quote or {}
    amount, missing = quote.get('total_cny'), quote.get('missing_count')
    if req.get('budget_basis') == 'new_purchase' and req.get('owned_parts'):
        amount, missing = quote.get('purchase_total_cny'), quote.get('purchase_missing_count', 0)
    checks['quote_complete'] = amount is not None and missing == 0
    upper = Decimal(req['budget_cny']) * (1 + Decimal(str(req.get('budget_flex', 0))))
    checks['explicit_budget'] = bool(checks['quote_complete'] and 0 < Decimal(amount) <= upper)
    locked = set(case.get('locked', [])) | set(case.get('change', {}).get('locked_categories', []))
    checks['locked_parts'] = all(selection.get(c) == case['base_selection']['parts'][c] for c in locked) if locked else None
    brands = dict(req.get('brand_pref', {}))
    if case.get('change', {}).get('intent') == 'swap_part':
        if 'AMD' not in case['change']['swap']['target_hint']:
            raise ValueError('Review new swap vendor intent explicitly')
        brands['gpu'] = 'amd'
    for category in ['cpu', 'gpu']:
        desired = brands.get(category)
        check = None
        if desired and desired != 'any':
            part = catalog.get(selection.get(category), {})
            if category == 'cpu':
                actual = part.get('brand', '').lower()
            else:
                name = part.get('model', '')
                actual = 'amd' if 'Radeon' in name else 'nvidia' if 'GeForce' in name else 'intel' if 'Arc ' in name else ''
            check = actual == desired
        checks[category + '_vendor'] = check
    checks['itx_motherboard'] = (catalog.get(selection.get('motherboard'), {}).get('specs', {}).get('form_factor') == 'itx') if req.get('size_pref') == 'itx' else None
    checks['owned_exact_models'] = None
    if req.get('owned_parts'):
        matched = True
        for owned in req['owned_parts']:
            category = owned['category']
            ids = (selection.get('ssd') or []) if category == 'ssd' else [{'sku': selection.get(category), 'quantity': 1}]
            exact = False
            for chosen in ids:
                part = catalog.get(chosen.get('sku'), {})
                names = [part.get('model', ''), (part.get('brand', '') + ' ' + part.get('model', '')).strip()]
                exact = exact or (owned['model'].casefold() in [n.casefold() for n in names]
                                  and (owned.get('quantity') or 1) == chosen.get('quantity'))
            matched = matched and exact
        checks['owned_exact_models'] = matched
    return checks


def legacy_record(row, case):
    result = row.get('result') or {}
    raw = (result.get('Draft') or {}).get('Selection') or {}
    selection = {k: raw.get(v) for k, v in {'cpu': 'CPU', 'gpu': 'GPU', 'motherboard': 'Motherboard', 'memory': 'Memory',
                                           'ssd': 'SSDs', 'psu': 'PSU', 'case': 'Case', 'cooler': 'Cooler'}.items()}
    candidates = row.get('snapshot', {}).get('catalog', {}).get('Candidates', [])
    catalog = {p['SKU']: {'brand': p['Brand'], 'model': p['Model'], 'specs': p['Specs']} for p in candidates}
    facts = result.get('Result') or {}
    checks = common_checks(case, selection, catalog, facts.get('Report'), facts.get('Quote'), result.get('Succeeded', False))
    return {'case_id': case['id'], 'seed': row['seed'], 'sample': 'historical-v1.5', 'checks': checks,
            'core_pass': all(v for v in checks.values() if v is not None),
            'model_calls': row['usage']['model_calls'], 'tokens': row['usage'].get('total_tokens'), 'duration_ms': row['duration_ms']}


def current_record(row, case, sample):
    plans = [step for step in row['steps'] if step.get('planning_input') is not None]
    if len(plans) != 1:
        raise ValueError('Shared Builder trial must have exactly one actual planning run: ' + case['id'])
    result = plans[0].get('result') or {}
    draft = result.get('draft') or {}
    catalog = {p['id']: p for p in result.get('candidates', [])}
    accepted = result.get('outcome') == 'ready' and result.get('delivery', {}).get('status') == 'delivered' and result.get('build_version', 0) > 0
    checks = common_checks(case, draft.get('selection', {}), catalog, result.get('validation'), result.get('quote'), accepted)
    traces = [t for step in row['steps'] for t in (step.get('trace') or []) if t.get('provider_called')]
    tokens = sum(t['tokens'] for t in traces) if all(t.get('tokens') is not None for t in traces) else None
    return {'case_id': case['id'], 'sample': sample, 'checks': checks,
            'core_pass': all(v for v in checks.values() if v is not None),
            'model_calls': len(traces), 'tokens': tokens, 'duration_ms': sum(s['duration_ms'] for s in row['steps'])}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--reports', type=Path, nargs='+', required=True)
    parser.add_argument('--out', type=Path, required=True)
    args = parser.parse_args()
    original = ROOT / 'internal/evalsuite/testdata'
    cases = {}
    for entry in json.loads((original / 'suites/v1.5.json').read_bytes())['cases']:
        case = json.loads((original / 'cases' / entry['file']).read_bytes())
        if canonical_hash(case) != entry['sha256']:
            raise ValueError('Historical case drift')
        cases[case['id']] = case
    legacy_path = ROOT / 'artifacts/eval/20260909-134447-3770035618/results.jsonl'
    hashes = {str(legacy_path.relative_to(ROOT)): hashlib.sha256(legacy_path.read_bytes()).hexdigest()}
    records, selected = [], set()
    for path in args.reports:
        raw = path.read_bytes()
        report = json.loads(raw)
        if report['mode'] != 'live_models_offline_tools':
            raise ValueError('Only actual real-model reports belong in this comparison')
        hashes[str(path)] = hashlib.sha256(raw).hexdigest()
        for row in report['cases']:
            case = cases[row['id']]
            if case['expect']['outcome'] not in {'pass', 'budget_adaptive'}:
                continue  # Retired refusal paths need a separate behavioral evaluation.
            selected.add(row['id'])
            records.append(current_record(row, case, path.parent.name))
    for line in legacy_path.read_text(encoding='utf-8').splitlines():
        row = json.loads(line)
        if row['case_id'] in selected:
            records.append(legacy_record(row, cases[row['case_id']]))
    groups = defaultdict(list)
    for row in records:
        groups[(row['sample'], row['case_id'])].append(row)
    summary = []
    for (sample, id), rows in sorted(groups.items()):
        summary.append({'sample': sample, 'case_id': id, 'passed': sum(r['core_pass'] for r in rows), 'runs': len(rows),
                        'mean_model_calls': sum(r['model_calls'] for r in rows)/len(rows),
                        'mean_tokens': sum(r['tokens'] for r in rows)/len(rows) if all(r['tokens'] is not None for r in rows) else None,
                        'mean_ms': sum(r['duration_ms'] for r in rows)/len(rows)})
    limitations = [
        'Common hardware/accounting subset only; not full rubric parity or prose/performance correctness.',
        'Use explicit original budget/flex, never the legacy decoder default uplift. Original grades remain untouched.',
        'Different catalog/price snapshots, execution architecture and repeat counts; costs are descriptive, not a causal model benchmark.',
        'Legacy accepted result means harness success, not proof of product persistence; new acceptance requires actual delivered version.',
        'Historical 3 repeats and every new batch are separate rows; no best-run selection.',
        'Missing user-model, purchase accounting or validation evidence fails; no assumed pass from a stored verdict.',
        'Cash cost unknown; token and logical model request counts are not invoice costs.',
    ]
    output = {'input_sha256': hashes, 'script_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
              'limitations': limitations, 'summary': summary, 'records': records}
    args.out.mkdir(parents=True, exist_ok=False)
    (args.out/'comparison.json').write_bytes((json.dumps(output, ensure_ascii=False, indent=2)+'\n').encode('utf-8'))
    print(json.dumps(summary, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    main()
