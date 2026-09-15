"""Offline checks for reviewed MSI specification blocks; no HTTP or publication."""
import unittest
from prepare_itx_intake import check_spec

# Minimal factual blocks from the 2026-09-15 saved official specification page.
TEXT = "\n".join(["MPG B650I EDGE WIFI", "Socket AM5", "Chipset\nAMD B650", "2x DDR5",
                  "Memory Support DDR5 7200+(OC)", "PCB Info\nMini-ITX", "Storage\n2x M.2"])


class ReviewedITXTests(unittest.TestCase):
    def test_exact_specs(self):
        _, fields = check_spec("msi-b650i-edge-wifi", TEXT)
        self.assertEqual({name: value for name, (value, _) in fields.items()}, {
            "socket": "AM5", "chipset": "B650", "memory_generation": "DDR5", "memory_speed_max_mts": 7200,
            "form_factor": "itx", "m2_slots": 2})

    def test_identity_or_specs_drift_stops_intake(self):
        for before, after in [("MPG B650I EDGE WIFI", "MPG B650I EDGE WIFI MAX"), ("DDR5", "DDR4"),
                              ("Mini-ITX", "ATX"), ("2x M.2", "1x M.2"), ("7200+(OC)", "6800(OC)")]:
            with self.subTest(field=before), self.assertRaises(ValueError):
                check_spec("msi-b650i-edge-wifi", TEXT.replace(before, after))


if __name__ == "__main__":
    unittest.main()
