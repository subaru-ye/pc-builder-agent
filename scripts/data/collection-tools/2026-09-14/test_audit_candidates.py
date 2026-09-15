import copy
import unittest
from audit_candidates import audit, read_rows, provisional_specs


class IntakeAuditTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.candidates=read_rows("candidates.jsonl")
        cls.offers=read_rows("offers.jsonl")

    def test_saved_cross_variant_failures(self):
        rows={r["model_id"]:r for r in audit(self.candidates,self.offers)}
        self.assertEqual(rows["cpu-i3-14100f"]["title_claims"]["form"],"散片")
        self.assertEqual(rows["gpu-rtx3050-8g-colorful"]["identity_status"],"conflict")
        self.assertEqual(rows["mb-msi-b850m-mortar-wifi"]["identity_status"],"conflict")
        self.assertEqual(rows["mem-gloway-tiance-ddr5-32-6000"]["identity_status"],"conflict")
        self.assertIsNone(rows["cooler-valkyrie-a360"]["title_claims"]["radiator_mm"])
        self.assertEqual(rows["gpu-rtx5070ti-zotac"]["title_claims"]["mem_gb"],16)
        self.assertTrue(all(r["publish_status"]=="candidate_only" for r in rows.values()))
        self.assertEqual(self.candidates[0]["specs"]["form"],"盒装", "must preserve collected source")

    def test_reviewed_quote_cannot_drift(self):
        offers=copy.deepcopy(self.offers)
        next(o for o in offers if o["model_id"]=="cpu-i3-14100f" and o["is_primary"])["title"]="other CPU"
        with self.assertRaisesRegex(ValueError,"quote drift"):
            audit(self.candidates,offers)

    def test_price_drift_or_duplicate_primary_fails(self):
        offers=copy.deepcopy(self.offers)
        next(o for o in offers if o["model_id"]=="cpu-i3-14100f" and o["is_primary"])["price_cny"]=1
        with self.assertRaisesRegex(ValueError,"price drift"):
            audit(self.candidates,offers)
        offers=copy.deepcopy(self.offers)
        offers.append(copy.deepcopy(next(o for o in offers if o["is_primary"])))
        with self.assertRaisesRegex(ValueError,"one original primary"):
            audit(self.candidates,offers)

    def test_cpu_core_count_does_not_fabricate_socket(self):
        specs=provisional_specs("cpu",{"cores":8,"threads":16,"form":"盒装"})
        self.assertTrue(all(v is None for v in specs.values()))


if __name__=="__main__":
    unittest.main()
