import unittest
from freeze_live_replay import screening_payload


class ScreeningPayloadTest(unittest.TestCase):
    def test_product_object_selection(self):
        for raw in ['{"operations":[]}', '```json\n{"operations":[]}\n```',
                    '说明 {not json}\n```json\n{"operations":[]}\n```',
                    '{"operations":[],"nested":{"schema_version":9}}']:
            self.assertEqual(screening_payload(raw)['operations'], [])
        self.assertEqual(screening_payload('{"first":true} {"schema_version":1}'), {'schema_version':1})
        self.assertEqual(screening_payload('{"operations":[]} {"operations":[1]}'), {'operations':[]})

    def test_malformed_payload_not_repaired(self):
        for raw in ['no object', '{"operations":[', '```json\n{invalid}\n```']:
            with self.assertRaises(ValueError):
                screening_payload(raw)


if __name__ == '__main__':
    unittest.main()
