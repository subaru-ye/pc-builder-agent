"""Add independently versioned negative assertions without rewriting old grades.

Recorded answers retain observed mistakes. Authored oracle answers check the
assertions/product path, never model accuracy. Live output has no answer oracle.
"""
import argparse
import copy
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
ORIGINAL = ROOT / 'internal/planningeval/testdata/legacy-screening-v1.5-20260915/offline/suite.json'
IDS = ('L5-301', 'L5-303')


def build(original, recorded):
    originals = {c['id']: c for c in original['cases']}
    recordings = {c['id']: c for c in recorded['cases']}
    if original['catalog'] != recorded['catalog']:
        raise ValueError('Catalog drift')
    variants = {name: {**copy.deepcopy(original), 'cases': [], 'live': name == 'live',
                      'version': 'screening-semantic-audit-20260915-' + name,
                      'provenance': 'Additional negative assertions; separate from original historical scores.'}
                for name in ('oracle', 'recorded', 'live')}
    additions = []
    for case_id in IDS:
        case = originals[case_id]
        saved = recordings[case_id]
        if len(case['steps']) != len(saved['steps']):
            raise ValueError('Step count drift')
        for index, (step, recording) in enumerate(zip(case['steps'], saved['steps'])):
            if any(step.get(k) != recording.get(k) for k in ('kind', 'text', 'expect')):
                raise ValueError('Original message or grade drift')
            if step['kind'] == 'message' and 'screen_oracle' not in recording:
                raise ValueError('Missing saved model answer')
        for name, suite in variants.items():
            item = copy.deepcopy(saved if name == 'recorded' else case)
            item['source'] = 'Original ' + case_id + '; additive semantic assertions, no original result replacement.'
            for index, step in enumerate(item['steps']):
                fields = ['budget_basis']
                # "Saw a quote" is not ownership. Only the initial unknown
                # state is exact: later explicit denial may remove or clear it.
                if case_id == 'L5-301' or index == 0:
                    fields += ['owned_parts', 'existing_parts']
                for field in fields:
                    expected = {'status': 'unknown'}
                    previous = step['expect']['fields'].get(field)
                    if previous is not None and previous != expected:
                        raise ValueError('Audit would replace an existing expectation')
                    step['expect']['fields'][field] = expected
                    if name == 'oracle':
                        additions.append({'case': case_id, 'step': index+1, 'field': field, 'expect': expected})
                if name == 'live':
                    step.pop('screen_oracle', None)
            suite['cases'].append(item)
    return variants, additions


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--recorded-suite', required=True, type=Path)
    parser.add_argument('--out', required=True, type=Path)
    args = parser.parse_args()
    raw = ORIGINAL.read_bytes()
    pin = json.loads(ORIGINAL.with_name('provenance.json').read_bytes())
    if hashlib.sha256(raw).hexdigest() != pin['suite_sha256']:
        raise ValueError('Original frozen suite drift')
    saved = args.recorded_suite.read_bytes()
    saved_pin = json.loads(args.recorded_suite.with_name('provenance.json').read_bytes())
    if hashlib.sha256(saved).hexdigest() != saved_pin['suite_sha256'] or saved_pin['expectations_changed']:
        raise ValueError('Saved answer suite drift')
    variants, additions = build(json.loads(raw), json.loads(saved))
    args.out.mkdir(parents=True, exist_ok=False)
    for name, suite in variants.items():
        folder = args.out / name
        folder.mkdir()
        data = (json.dumps(suite, ensure_ascii=False, indent=2)+'\n').encode('utf-8')
        (folder/'suite.json').write_bytes(data)
        provenance = {'suite_sha256': hashlib.sha256(data).hexdigest(),
                      'original_suite_sha256': pin['suite_sha256'],
                      'recorded_suite_sha256': saved_pin['suite_sha256'],
                      'source_recording': saved_pin, 'added_assertions': additions,
                      'original_expectations_preserved': True, 'original_results_modified': False,
                      'mode': name, 'new_model_requests': 0, 'generator_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}
        (folder/'provenance.json').write_bytes((json.dumps(provenance, ensure_ascii=False, indent=2)+'\n').encode('utf-8'))
    print('Frozen 2 cases / 5 message turns; original expectations retained, no model calls')


if __name__ == '__main__':
    main()
