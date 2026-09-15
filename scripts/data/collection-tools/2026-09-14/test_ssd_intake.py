import gzip
import json
import unittest
from audit_candidates import ROOT
from fetch_intake_specs import decode_page
from pcdata.automation import _VisiblePolicyTextParser
from prepare_ssd_intake import check_spec
from prepare_gpu_intake import check_spec as check_gpu_spec


class SSDCaptureTest(unittest.TestCase):
    def test_saved_exact_gpu_board_and_spec_drift(self):
        folder=ROOT/"artifacts/data-intake-official-20260915-msi-5060ti-8g-ventus3x-oc"
        if not folder.exists():
            self.skipTest("saved official capture archive is not present")
        parser=_VisiblePolicyTextParser()
        parser.feed(decode_page((folder/"msi-5060ti-8g-ventus3x-oc.html").read_bytes()))
        text="\n".join(parser.parts)
        _, fields=check_gpu_spec("msi-5060ti-8g-ventus3x-oc",text)
        self.assertEqual({key:value[0] for key,value in fields.items()},
                         {"tdp_w":180,"power_connectors":["pcie_8pin"],"length_mm":304})
        for before,after in (("8G VENTUS","16G VENTUS"),("G506T-8V3C","G506T-16V3C"),
                             ("180 W","200 W"),("8-pin x 1","8-pin x 2"),("304 x 121","303 x 121")):
            with self.subTest(field=before), self.assertRaises(ValueError):
                check_gpu_spec("msi-5060ti-8g-ventus3x-oc",text.replace(before,after))

    def test_saved_official_capacity_blocks(self):
        folder=ROOT/"artifacts/data-intake-official-20260914-r3"
        if not folder.exists():
            self.skipTest("saved official capture archive is not present")
        for key in ("zhitai-ti600","wd-sn7100"):
            parser=_VisiblePolicyTextParser()
            parser.feed(decode_page((folder/(key+".html")).read_bytes()))
            text="\n".join(parser.parts)
            check_spec(key,text)
            for altered in (text.replace("M.2 2280","2.5 inch"),text.replace("500GB","250GB")):
                with self.assertRaises(ValueError): check_spec(key,altered)

    def test_compressed_limit_and_plain_text(self):
        self.assertEqual(decode_page(gzip.compress("正式规格".encode())),"正式规格")
        self.assertEqual(decode_page(b"plain"),"plain")
        with self.assertRaisesRegex(ValueError,"4 MiB"):
            decode_page(gzip.compress(b"x"*(4*1024*1024+1)))


if __name__=="__main__":unittest.main()
