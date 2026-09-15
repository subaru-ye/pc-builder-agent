import copy
import json
import unittest

from build_screening_semantic_audit import ORIGINAL, build


class SemanticAuditTests(unittest.TestCase):
    def setUp(self):
        self.original = json.loads(ORIGINAL.read_bytes())

    def test_additive_grades_and_no_answers_in_live(self):
        before = copy.deepcopy(self.original)
        variants, additions = build(self.original, self.original)
        self.assertEqual(self.original, before)
        original = {c['id']: c for c in self.original['cases']}
        self.assertTrue(additions)
        for mode, suite in variants.items():
            for case in suite['cases']:
                for old, new in zip(original[case['id']]['steps'], case['steps']):
                    self.assertEqual(old.get('text'), new.get('text'))
                    for name, value in old['expect'].items():
                        if name == 'fields':
                            for field, expectation in value.items():
                                self.assertEqual(new['expect']['fields'][field], expectation)
                        else:
                            self.assertEqual(new['expect'][name], value)
                    if mode == 'live':
                        self.assertNotIn('screen_oracle', new)
                    else:
                        self.assertEqual(old.get('screen_oracle'), new.get('screen_oracle'))

    def test_changed_source_is_rejected(self):
        for path in ('text', 'expect', 'answer', 'catalog'):
            with self.subTest(path=path):
                saved = copy.deepcopy(self.original)
                step = next(c for c in saved['cases'] if c['id'] == 'L5-301')['steps'][0]
                if path == 'text': step['text'] += ' changed'
                elif path == 'expect': step['expect']['versions'] = 9
                elif path == 'answer': step.pop('screen_oracle')
                else: saved['catalog']['date'] = '1999-01-01'
                with self.assertRaises(ValueError): build(self.original, saved)

    def test_existing_assertion_cannot_be_weakened(self):
        case = next(c for c in self.original['cases'] if c['id'] == 'L5-301')
        case['steps'][0]['expect']['fields']['budget_basis'] = {'status': 'active', 'value': 'new_purchase'}
        with self.assertRaises(ValueError): build(self.original, self.original)


if __name__ == '__main__':
    unittest.main()
