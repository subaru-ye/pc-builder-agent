"""Version the explicit protocol oracle for one unresolved-proposal review.

Old fixtures and real answers remain untouched. Appended answers are deliberate
offline oracles repeating the unresolved result, not additional model recordings.
"""
import argparse
import copy
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--out', required=True, type=Path)
    args = parser.parse_args()
    source = ROOT / 'internal/planningeval/testdata/v2.0.json'
    raw = source.read_bytes()
    provenance = json.loads(source.with_name('provenance.json').read_bytes())
    if hashlib.sha256(raw).hexdigest() != provenance['suite_sha256']:
        raise ValueError('Frozen protocol fixture changed')
    suite = json.loads(raw)
    suite['version'] = 'planning-v2.1-proposal-review-offline'
    additions = []
    for case in suite['cases']:
        for index, step in enumerate(case['steps']):
            outputs = step.get('builder_oracle', [])
            # The previous ready-result correction in P2-011 already supplies a
            # second final response. Do not add another review there.
            finals = []
            for output in outputs:
                parts = output.get('parts', [])
                if len(parts) == 1 and parts[0].get('text'):
                    finals.append(json.loads(parts[0]['text']))
            if (len(finals) == 1 and finals[0].get('outcome') == 'proposal'
                    and finals[0].get('issues') and len(outputs) <= 6):
                outputs.append(copy.deepcopy(outputs[-1]))
                if 'builder_calls' in step['expect']:
                    step['expect']['builder_calls'] += 1
                additions.append({'case': case['id'], 'step': index,
                                  'change': 'Repeat final unresolved proposal once as an authored review response'})
    data = (json.dumps(suite, ensure_ascii=False, indent=2) + '\n').encode('utf-8')
    args.out.mkdir(parents=True, exist_ok=False)
    (args.out / 'suite.json').write_bytes(data)
    manifest = {'source_suite_sha256': hashlib.sha256(raw).hexdigest(),
                'suite_sha256': hashlib.sha256(data).hexdigest(),
                'generator_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                'changes': additions, 'grading_changes': 'Only expected oracle call counts; state, evidence, version and delivery assertions unchanged',
                'actual_model_requests': 0}
    (args.out / 'provenance.json').write_bytes((json.dumps(manifest, ensure_ascii=False, indent=2) + '\n').encode('utf-8'))
    print('Versioned', len(additions), 'explicit proposal review responses')


if __name__ == '__main__':
    main()
