"""Prepare two reviewed SFX/SFX-L PSU variants from saved official pages.

Thermalright TR-SGFX650 is deferred: its official page publishes wattage and
dimensions but no power-connector counts, so `power_connectors` has no
traceable evidence. FSP Dagger SD-650GM and DeepCool PS750G stay deferred:
official product pages were unreachable.

No network calls. The 2026-09-17 buyer quotes keep their observation date.
"""
import argparse
import csv
import hashlib
import io
import json
import re
import sys
from decimal import Decimal
from pathlib import Path

import yaml

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE / ".." / "2026-09-14"))

from audit_candidates import ROOT, digest
from prepare_amd_intake import jsonl
from prepare_ssd_intake import prepare

BATCH = ROOT / "var/data/collections/2026-09-17-sfx-psu-01"

DEFERRED = {
    "psu-thermalright-tr-sgfx650": "官网规格页(thermalright.com/product/sgfx650/)公布额定650W与尺寸，但未公布供电接口数量，power_connectors 无可追溯证据",
    "psu-fsp-dagger-sd650gm": "fsptech.com HTTPS 不可达且无 Dagger 产品页，无可追溯官网规格",
    "psu-deepcool-ps750g": "cn.deepcool.com 与全球站均无 PS750G 产品页，无可追溯官网规格",
}


def sq(s: str) -> str:
    s = str(s or "").lower()
    s = re.sub(r"g\s*[x×*]\s*(\d)", r"g\1", s)
    return re.sub(r"[^a-z0-9\u4e00-\u9fff]", "", s)


# sku, page_key, brand, model, search/detail goodsId, needle in the buyer title
CANDIDATES = [
    ("psu-cm-v850sfx-gold-white", "cm-v-sfx-gold-850-white", "Cooler Master",
     "V SFX Gold 850W ATX 3.1 White Edition",
     "ejQZew6fEXG04SgmCd1l04SgmCd1lQ_3vbTPorgpRZ4sRJBrY", "V SFX 850GOLD 白 ATX3.1电源"),
    ("psu-asus-rog-loki-850p-white", "asus-rog-loki-850p-white", "ASUS",
     "ROG Loki SFX-L 850P White",
     "qp4cyXdLsbBYtDnyVlEOYtDnyVlEO4_3JKSxQ3GEqctDaWtCm", "ROG洛基850W SFX-L白色电源"),
]


def check_spec(key, text):
    if key == "cm-v-sfx-gold-850-white":
        required = ["V SFX Gold 850W ATX 3.1 White Edition",
                    "Form Factor\nSFX",
                    "Dimensions (L x W x H)\n100 x 125 x 63.5 mm",
                    "Watts\n850W",
                    "PCI-e 6+2 Pin Connectors\n4",
                    "12V-2x6 Connectors\n1"]
        fields = {
            "form_factor": ("sfx", required[1]),
            "length_mm": (100, required[2]),
            "wattage_w": (850, required[3]),
            "power_connectors": (["pcie_8pin"] * 4 + ["pcie_16pin"], required[4] + "; " + required[5]),
        }
    elif key == "asus-rog-loki-850p-white":
        required = ["ROG LOKI 洛基 850W SFX-L 白金牌电源",
                    "SFX-L",
                    "外形尺寸\n125 x 125 x 63.5 mm",
                    "总功率\n850W",
                    "PCI-E 16-pin x 1",
                    "PCI-E 8-pin x 3"]
        fields = {
            "form_factor": ("sfx_l", required[1]),
            "length_mm": (125, required[2]),
            "wattage_w": (850, required[3]),
            "power_connectors": (["pcie_16pin", "pcie_8pin", "pcie_8pin", "pcie_8pin"], required[4] + "; " + required[5]),
        }
    else:
        raise ValueError("unreviewed psu page")
    if required[0] not in text or any(block not in text for block in required):
        raise ValueError("reviewed official identity/specification changed")
    return required, fields


def verify_buyer_rows():
    index = [json.loads(l) for l in (BATCH / "raw_index.jsonl").read_text(encoding="utf-8").splitlines() if l.strip()]
    details = {d["detail_id"]: d for d in index if d["kind"] == "detail"}
    search_rows = {}
    for rec in index:
        if rec["kind"] != "search" or not rec["ok"]:
            continue
        path = BATCH / rec["file"]
        if digest(path) != rec["sha256"]:
            raise ValueError("buyer search csv hash changed")
        for r in csv.DictReader(io.StringIO(path.read_text(encoding="utf-8"))):
            search_rows[(r["source"], r["goodsId"])] = (r, path)
    bindings = []
    for sku, key, brand, model, goods_id, needle in CANDIDATES:
        detail_id = sku + "__r1"
        rec = details.get(detail_id)
        if not rec or rec["goodsId"] != goods_id or str(rec["source"]) != "2":
            raise ValueError(f"{sku}: detail request binding missing")
        detail_path = BATCH / rec["file"]
        if digest(detail_path) != rec["sha256"]:
            raise ValueError(f"{sku}: saved detail hash changed")
        match = search_rows.get(("2", goods_id))
        if not match:
            raise ValueError(f"{sku}: buyer search row missing")
        row, csv_path = match
        detail = yaml.safe_load(detail_path.read_text(encoding="utf-8"))
        body = detail["商品详情"]
        if sq(detail["商品标题"]) != sq(row["title"]) or needle not in row["title"] or needle not in detail["商品标题"]:
            raise ValueError(f"{sku}: reviewed title drift")
        if str(body["platformId"]) != row["source"] or body["shopName"] != row["shopName"] \
                or Decimal(str(body["actualPrice"])) != Decimal(row["actualPrice"]) or not detail["购买链接"]:
            raise ValueError(f"{sku}: seller/price/link mismatch between search row and saved detail")
        bindings.append({
            "sku": sku, "page_key": key, "brand": brand, "model": model,
            "row_key": hashlib.sha256(f"{row['source']}|{row['goodsId']}|{sq(row['title'])}".encode()).hexdigest()[:16],
            "needle": needle, "goods_id": goods_id,
            "offer": {
                "row_key": None, "model_id": sku, "category": "psu",
                "platform_src": row["source"], "goodsId": row["goodsId"],
                "title": row["title"], "price_cny": row["actualPrice"],
                "original_cny": row["originalPrice"], "shop": row["shopName"],
                "month_sales": row["monthSales"], "buy_url": detail["购买链接"],
                "detail_observed_date": rec["ts"][:10],
                "detail_source_file": detail_path.relative_to(ROOT).as_posix(),
                "detail_source_sha256": rec["sha256"],
                "detail_request_goods_id": goods_id,
                "detail_response_goods_id": str(body["goodsId"]),
                "original_search_csv": csv_path.relative_to(ROOT).as_posix(),
                "original_search_csv_sha256": digest(csv_path),
            },
            "note": "Opaque response goodsId differs from the request goodsId; the exact title, seller, purchase link, price and the saved request index bind this observation. No claim of fresh price or stock.",
        })
    return bindings


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--pages", required=True, type=Path)
    parser.add_argument("--out", required=True, type=Path)
    args = parser.parse_args()
    bindings = verify_buyer_rows()
    args.out.mkdir(parents=True, exist_ok=False)
    source = args.out / "reviewed-offers.jsonl"
    jsonl(source, [dict(b["offer"], row_key=b["row_key"]) for b in bindings])
    choices = [(b["sku"], b["page_key"], b["brand"], b["model"], b["row_key"], b["needle"]) for b in bindings]
    prepare(args.out, args.pages, choices=choices, check=check_spec, category="psu",
            offer_source=source, offer_sha256=digest(source))
    (args.out / "detail-review.json").write_bytes((json.dumps(
        {"batch_dir": BATCH.relative_to(ROOT).as_posix(), "bindings": bindings,
         "deferred": DEFERRED}, ensure_ascii=False, indent=2) + "\n").encode("utf-8"))


if __name__ == "__main__":
    main()
