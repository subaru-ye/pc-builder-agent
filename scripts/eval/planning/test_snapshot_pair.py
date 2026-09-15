import copy
import json
from pathlib import Path
import tempfile
import unittest

from build_snapshot_pair import catalog, cases, load_pinned, ROOT


class FrozenSnapshotPairTest(unittest.TestCase):
    def test_checked_in_pair_matches_provenance_and_reviewed_changes(self):
        suites = {}
        for side in ('old', 'new'):
            folder = ROOT / 'internal/planningeval/testdata/data-pair-20260915' / side
            provenance = json.loads((folder / 'provenance.json').read_text(encoding='utf-8'))
            suites[side] = load_pinned(folder / 'suite.json', provenance['suite_sha256'])
        old, new = suites['old'], suites['new']
        self.assertEqual((len(old['catalog']['candidates']), len(new['catalog']['candidates'])), (160, 172))
        self.assertEqual((len(old['catalog']['price_metadata']), len(new['catalog']['price_metadata'])), (160, 120))
        for a, b in zip(old['cases'], new['cases']):
            self.assertEqual(len(a['steps']), len(b['steps']))
            for x, y in zip(a['steps'], b['steps']):
                self.assertEqual({k:v for k,v in x.items() if k != 'expect'},
                                 {k:v for k,v in y.items() if k != 'expect'})

    def test_explicit_batch_no_price_fallback_and_metadata(self):
        data = {'snapshots': [{'id': 1, 'snapshot_date': '2026-09-14'}, {'id': 2, 'snapshot_date': '2026-09-14'}],
                'parts': [{'sku': 'cpu', 'active': True, 'catalog_state': 'active_core',
                           'category': 'cpu', 'brand': 'AMD', 'model': 'Test', 'specs': {'socket': 'AM4'}}],
                'evidence': [], 'prices': [{'snapshot_id': 1, 'sku': 'cpu', 'price_cny': 100,
                    'source': 'aggregator', 'observed_at': '2026-09-08T00:00:00Z',
                    'price_type': 'listing', 'availability_basis': 'unknown'}]}
        before, after = catalog(data, 1), catalog(data, 2)
        self.assertEqual(before['candidates'][0]['price_cny'], '100')
        self.assertIsNone(after['candidates'][0]['price_cny'])
        self.assertEqual(before['price_metadata']['cpu']['observed_at'], '2026-09-08T00:00:00Z')
        self.assertEqual(before['price_metadata']['cpu']['availability_basis'], 'unknown')
        self.assertEqual(after['price_metadata'], {})
        self.assertEqual(before['candidates'][0]['specs'], data['parts'][0]['specs'])
        bad = copy.deepcopy(data)
        bad['prices'].append(copy.deepcopy(bad['prices'][0]))
        with self.assertRaisesRegex(ValueError, 'duplicate quote'): catalog(bad, 1)
        with self.assertRaises(ValueError): catalog(data, 3)

    def test_oracle_and_user_inputs_identical_between_sides(self):
        saved = json.loads((ROOT / 'internal/planning/testdata/complete_proposal_recording.json').read_text(encoding='utf-8'))['result']
        old, new = cases(saved, False), cases(saved, True)
        for before, after in zip(old, new):
            for a, b in zip(before['steps'], after['steps']):
                self.assertEqual({k:v for k,v in a.items() if k != 'expect'},
                                 {k:v for k,v in b.items() if k != 'expect'})

    def test_hash_drift_rejected(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / 'snapshot.json'
            path.write_bytes(b'{}')
            with self.assertRaisesRegex(ValueError, 'hash mismatch'): load_pinned(path, '0' * 64)


if __name__ == '__main__': unittest.main()
