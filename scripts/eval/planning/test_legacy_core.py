import unittest

from compare_legacy_core import RULES, common_checks


class SharedCoreTest(unittest.TestCase):
    def check(self, case, **kw):
        facts = {'overall_status': 'pass', 'checks': [{'rule_id': r, 'outcome': 'pass'} for r in RULES]}
        return common_checks(case, kw.get('selection', {}), kw.get('catalog', {}), facts,
                             kw.get('quote', {'total_cny': '8236.00', 'missing_count': 0}), True)

    def test_only_explicit_budget_flex(self):
        original = {'requirement': {'budget_cny': 8000}}
        self.assertFalse(self.check(original)['explicit_budget'])
        original['requirement']['budget_flex'] = 0.1
        self.assertTrue(self.check(original)['explicit_budget'])

    def test_locked_sku_not_satisfied_by_compatible_replacement(self):
        case = {'requirement': {'budget_cny': 9000}, 'locked': ['gpu'], 'base_selection': {'parts': {'gpu': 'original'}}}
        self.assertFalse(self.check(case, selection={'gpu': 'alternative'})['locked_parts'])
        self.assertTrue(self.check(case, selection={'gpu': 'original'})['locked_parts'])

    def test_empty_or_invalid_historical_amount_is_not_complete(self):
        for value in ['', None, 'unknown', 'NaN', 'Infinity', '-1']:
            with self.subTest(value=value):
                result = self.check({'requirement': {'budget_cny': 8000}}, quote={'total_cny': value, 'missing_count': 0})
                self.assertFalse(result['quote_complete'])
                self.assertFalse(result['explicit_budget'])

    def test_purchase_total_and_exact_owned_model_both_required(self):
        case = {'requirement': {'budget_cny': 6000, 'budget_basis': 'new_purchase', 'owned_parts': [{'category': 'cpu', 'model': 'AMD Ryzen 5 7600'}]}}
        kwargs = {'selection': {'cpu': 'c'}, 'catalog': {'c': {'brand': 'AMD', 'model': 'Ryzen 5 7600'}}}
        self.assertFalse(self.check(case, **kwargs)['explicit_budget'])
        result = self.check(case, quote={'total_cny': '8000', 'missing_count': 0, 'purchase_total_cny': '5000', 'purchase_missing_count': 0}, **kwargs)
        self.assertTrue(result['explicit_budget'])
        self.assertTrue(result['owned_exact_models'])
        kwargs['catalog']['c']['model'] = 'Ryzen 5 7500F'
        self.assertFalse(self.check(case, **kwargs)['owned_exact_models'])


if __name__ == '__main__':
    unittest.main()
