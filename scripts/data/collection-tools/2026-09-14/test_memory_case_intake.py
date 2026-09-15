import copy
import json
import unittest

from audit_candidates import ROOT, read_rows
from prepare_memory_case_intake import check_spec, reviewed_detail


class MemoryCaseReviewTest(unittest.TestCase):
    def test_saved_specs_and_exact_memory_kit(self):
        folder = ROOT/'artifacts/data-intake-official-memory-case-20260915-r1'
        if not folder.exists(): self.skipTest('official capture archive unavailable')
        for key, changes in {
            'kingbank-xingren-black': [('48GB(24GBx2)', '32GB(16GBx2)'), ('CL28', 'CL36'),
                                      ('K5.01.FLM5EM9502', 'K5.01.FLM5GM9502')],
            'lianli-lancool207': [('375mm (Max.)', '370mm (Max.)'), ('180mm (Max.)', '170mm (Max.)'),
                                 ('Top: 360/280/240mm', 'Top: 240mm'), ('Under 160mm', 'Under 180mm')],
        }.items():
            text = (folder/(key+'.txt')).read_text(encoding='utf-8')
            check_spec(key, text)
            for old, new in changes:
                with self.subTest(key=key, changed=old), self.assertRaises(ValueError):
                    check_spec(key, text.replace(old, new))

    def test_detail_provenance_and_new_observation(self):
        folder = ROOT/'artifacts/data-intake-reviewed-details-20260915-r1'
        if not folder.exists(): self.skipTest('detail capture archive unavailable')
        index = [json.loads(line) for line in (folder/'raw_index.jsonl').read_text(encoding='utf-8').splitlines()]
        offer = next(o for o in read_rows('offers.jsonl') if o['row_key']=='f86260e8a97e7a86')
        original = copy.deepcopy(offer)
        item = next(i for i in index if i['detail_id']==offer['model_id'])
        reviewed = reviewed_detail(offer, item, folder)
        self.assertEqual(offer, original)
        self.assertEqual(str(offer['price_cny']), '513.29')
        self.assertEqual(reviewed['price_cny'], '584.29')
        self.assertEqual(reviewed['detail_observed_date'], '2026-09-15')
        self.assertTrue(reviewed['buy_url'].startswith('https://m.tb.cn/'))
        for altered in ({**item, 'goodsId':'wrong'}, {**item, 'sha256':'0'*64}, {**item, 'source':'2'}):
            with self.assertRaises(ValueError): reviewed_detail(offer, altered, folder)


if __name__ == '__main__': unittest.main()
