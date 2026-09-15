import unittest
from compare_data_snapshot import fixed_quote, compare


class FixedQuoteTest(unittest.TestCase):
    def test_same_day_requires_explicit_batch(self):
        data={"snapshots":[{"snapshot_date":"2026-09-14","id":1},{"snapshot_date":"2026-09-14","id":2}]}
        with self.assertRaisesRegex(ValueError,"ambiguous"):
            compare(data,"2026-09-14","2026-09-14")

    def test_quantity_and_missing_are_not_zero_cost(self):
        prices={"ssd":{"price_cny":"10.25"}}
        result=fixed_quote(prices,[{"sku":"ssd","quantity":2},{"sku":"cpu","quantity":1}])
        self.assertEqual(result["known_subtotal"],"20.50")
        self.assertIsNone(result["complete_total"])
        self.assertEqual(result["missing"],["cpu"])

    def test_invalid_quantity(self):
        for quantity in [True,0,-1,1.5]:
            with self.assertRaises(ValueError):
                fixed_quote({},[{"sku":"ssd","quantity":quantity}])


if __name__=="__main__":unittest.main()
