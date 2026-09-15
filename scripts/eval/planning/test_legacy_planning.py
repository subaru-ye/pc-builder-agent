"""Input fidelity checks independent of model outcomes."""
import json
import unittest

from build_legacy_planning import ROOT, build_case


class HistoricalMigrationTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.catalog = json.loads((ROOT / 'internal/planningeval/testdata/live-smoke-20260915/suite.json').read_bytes())['catalog']

    def case(self, id):
        return json.loads((ROOT / 'internal/evalsuite/testdata/cases' / (id + '.json')).read_bytes())

    def test_original_modification_preconditions_and_locks(self):
        original = self.case('L2-107')
        migrated = build_case(original, self.catalog)
        self.assertEqual(migrated['previous_build']['selection'], original['base_selection'])
        self.assertEqual(migrated['previous_build']['requirement']['budget_cny'], 15000)
        self.assertEqual(migrated['steps'][1]['expect']['budget_ceiling_cny'], '14200')
        self.assertEqual(migrated['steps'][1]['expect']['selected_parts'], {'cpu': 'cpu-r7-9800x3d', 'gpu': 'gpu-gb-5070-windforce-sff'})
        change = next(op['value'] for op in migrated['steps'][0]['edit'] if op['field'] == 'free.historical_change')
        self.assertEqual(json.loads(change), original['change'])

    def test_no_default_preferences_or_fake_screening(self):
        migrated = build_case(self.case('L1-007'), self.catalog)
        fields = migrated['steps'][1]['expect']['fields']
        for key in ['noise_pref', 'budget_flex', 'brand_pref.cpu', 'brand_pref.gpu', 'use_case.resolution']:
            self.assertNotIn(key, fields)
        self.assertEqual([s['kind'] for s in migrated['steps']], ['edit', 'confirm', 'refresh'])
        self.assertFalse(any('screen_oracle' in s or 'builder_oracle' in s for s in migrated['steps']))

    def test_explicit_flex_and_purchase_basis(self):
        flexible = build_case(self.case('L1-013'), self.catalog)
        self.assertEqual(flexible['steps'][1]['expect']['budget_ceiling_cny'], '17250.00')
        owned = build_case(self.case('L4-206'), self.catalog)
        self.assertTrue(owned['steps'][1]['expect']['purchase_budget'])
        self.assertEqual(owned['steps'][1]['expect']['fields']['owned_parts']['value'], self.case('L4-206')['requirement']['owned_parts'])

    def test_retired_rejection_still_requires_execution_evidence(self):
        for id in ['L4-203', 'L4-205']:
            expect = build_case(self.case(id), self.catalog)['steps'][1]['expect']
            self.assertEqual(expect['versions'], 0)
            self.assertEqual(expect['require_tools'], ['search_local', 'evaluate'])
            self.assertTrue(expect['issues_any'])
            self.assertNotIn('ready', expect['outcome_one_of'])


if __name__ == '__main__':
    unittest.main()
