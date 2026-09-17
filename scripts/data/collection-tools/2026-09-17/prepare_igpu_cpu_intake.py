"""Prepare the reviewed AM4 iGPU CPU (Ryzen 5 4600G) from saved official page.

Ryzen 5 5600G and Ryzen 7 5700G stay deferred: their official driver/downloads
spec pages publish name, TDP, socket and graphics but no "Supporting Chipsets"
label, so `supported_chipsets` has no traceable evidence. No network calls.
The 2026-09-17 buyer quote keeps its observation date.
"""
import argparse
import hashlib
import json
import sys
from decimal import Decimal
from pathlib import Path

import yaml

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE / ".." / "2026-09-14"))

from audit_candidates import ROOT, digest
from prepare_amd_intake import jsonl
from prepare_ssd_intake import prepare

BATCH = ROOT / "var/data/collections/2026-09-17-igpu-cpu-01"

DEFERRED = {
    "cpu-r5-5600g": "官网规格页(amd.com/en/support/downloads/drivers.html/processors/ryzen/ryzen-5000-series/amd-ryzen-5-5600g.html)公布名称/默认TDP 65W/CPU Socket AM4/Graphics Model Radeon Graphics，但未公布 Supporting Chipsets，supported_chipsets 无可追溯证据",
    "cpu-r7-5700g": "官网规格页(amd.com/en/support/downloads/drivers.html/processors/ryzen/ryzen-5000-series/amd-ryzen-7-5700g.html)公布名称/默认TDP 65W/CPU Socket AM4/Graphics Model Radeon Graphics，但未公布 Supporting Chipsets，supported_chipsets 无可追溯证据",
}


def sq(s: str) -> str:
    s = str(s or "").lower()
    return s


# sku, page_key, brand, model, search/detail goodsId, needle in the buyer title
CANDIDATES = [
    ("cpu-r5-4600g", "amd-4600g", "AMD", "Ryzen 5 4600G",
     "gq7RKw3F3t2NO33QwqI787tatr-pb0NY4Pi2pa9vWzecj7", "R5-4600G"),
]


def check_spec(key, text):
    if key == "amd-4600g":
        chipsets = "X570 , X470 , X370 , B550 , B450 , B350 , A520 , A320"
        required = ["AMD Ryzen™ 5 4600G",
                    "CPU Socket\nAM4",
                    "Supporting Chipsets\n" + chipsets,
                    "Default TDP\n65W",
                    "Graphics Model\nRadeon™ Graphics"]
        fields = {
            "socket": ("AM4", required[1]),
            "supported_chipsets": (chipsets.replace(" , ", ",").split(","), required[2]),
            "tdp_w": (65, required[3]),
            "has_igpu": (True, required[4]),
        }
    else:
        raise ValueError("unreviewed cpu page")
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
        import csv, io
        for r in csv.DictReader(io.StringIO(path.read_text(encoding="utf-8"))):
            search_rows[(r["source"], r["goodsId"])] = (r, path)
    bindings = []
    for sku, key, brand, model, goods_id, needle in CANDIDATES:
        detail_id = sku + "__r1"
        rec = details.get(detail_id)
        if not rec or rec["goodsId"] != goods_id or str(rec["source"]) != "1":
            raise ValueError(f"{sku}: detail request binding missing")
        detail_path = BATCH / rec["file"]
        if digest(detail_path) != rec["sha256"]:
            raise ValueError(f"{sku}: saved detail hash changed")
        match = search_rows.get(("1", goods_id))
        if not match:
            raise ValueError(f"{sku}: buyer search row missing")
        row, csv_path = match
        detail = yaml.safe_load(detail_path.read_text(encoding="utf-8"))
        body = detail["商品详情"]
        if str(detail["商品标题"]) != str(row["title"]) or needle not in row["title"] or needle not in detail["商品标题"]:
            raise ValueError(f"{sku}: reviewed title drift")
        if str(body["platformId"]) != row["source"] or body["shopName"] != row["shopName"] \
                or Decimal(str(body["actualPrice"])) != Decimal(row["actualPrice"]) or not detail["购买链接"]:
            raise ValueError(f"{sku}: seller/price/link mismatch between search row and saved detail")
        bindings.append({
            "sku": sku, "page_key": key, "brand": brand, "model": model,
            "row_key": hashlib.sha256(f"{row['source']}|{row['goodsId']}|{row['title']}".encode()).hexdigest()[:16],
            "needle": needle, "goods_id": goods_id,
            "offer": {
                "row_key": None, "model_id": sku, "category": "cpu",
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
            "note": "Opaque response goodsId differs from the request goodsId; the exact title, seller, purchase link, price and the saved request index bind this observation. No claim of fresh price or stock. Buyer title does not state packaging; recorded as unknown.",
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
    prepare(args.out, args.pages, choices=choices, check=check_spec, category="cpu",
            offer_source=source, offer_sha256=digest(source))
    (args.out / "detail-review.json").write_bytes((json.dumps(
        {"batch_dir": BATCH.relative_to(ROOT).as_posix(), "bindings": bindings,
         "deferred": DEFERRED}, ensure_ascii=False, indent=2) + "\n").encode("utf-8"))


if __name__ == "__main__":
    main()
