"""Freeze a targeted new batch after restoring complete operation examples."""
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]


def main():
    source = ROOT / 'internal/planningeval/testdata/legacy-screening-v1.5-20260915/live/suite.json'
    raw = source.read_bytes()
    pin = json.loads(source.with_name('provenance.json').read_bytes())
    if hashlib.sha256(raw).hexdigest() != pin['suite_sha256']:
        raise ValueError('Frozen source suite changed')
    suite = json.loads(raw)
    ids = ['L3-103', 'L3-104', 'L5-301', 'L5-306', 'L5-308']
    suite['cases'] = [case for case in suite['cases'] if case['id'] in ids]
    suite['version'] = 'screening-protocol-recheck-20260915'
    suite['provenance'] = 'Targeted new live batch: operation discriminator, evidence shape, budget/owned-model updates. Original scenario grades retained, no oracle.'
    calls = sum(step['kind'] == 'message' for case in suite['cases'] for step in case['steps'])
    if calls != 9 or len(suite['cases']) != len(ids):
        raise ValueError('Declared targeted batch changed')
    output = ROOT / 'internal/planningeval/testdata/screening-protocol-recheck-20260915'
    output.mkdir(exist_ok=False)
    data = (json.dumps(suite, ensure_ascii=False, indent=2) + '\n').encode('utf-8')
    (output / 'suite.json').write_bytes(data)
    provenance = {'source_suite_sha256': pin['suite_sha256'], 'suite_sha256': hashlib.sha256(data).hexdigest(),
                  'max_model_requests': 9, 'external_requests': 0, 'embedding_requests': 0,
                  'selected_cases': ids, 'grades_changed': False,
                  'known_grader_limitation': 'Omitted owned_parts.quantity defaults to one in business code but original exact JSON grade rejects it; retain and report separately',
                  'generator_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}
    (output / 'provenance.json').write_bytes((json.dumps(provenance, ensure_ascii=False, indent=2) + '\n').encode('utf-8'))
    print('Frozen 5 cases / 9 Screening requests; exact original grades, no external requests')


if __name__ == '__main__':
    main()
