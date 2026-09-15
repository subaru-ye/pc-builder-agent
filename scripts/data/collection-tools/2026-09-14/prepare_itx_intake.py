"""Review one exact ITX board using saved official specs and original buyer detail.

No network calls. The original 2026-09-13 quote keeps its observation date.
"""
import argparse
import json
from decimal import Decimal
from pathlib import Path
import yaml

from audit_candidates import ROOT, digest, read_rows
from prepare_amd_intake import jsonl
from prepare_ssd_intake import prepare

SKU = "mb-msi-b650i-edge"
ROW_KEY = "6ef4311fbdde68cc"
CHOICES = [(SKU, "msi-b650i-edge-wifi", "MSI", "MPG B650I EDGE WIFI", ROW_KEY, "微星MPG B650I EDGE WIFI AM5刀锋主板支持")]


def check_spec(key, text):
    if key != "msi-b650i-edge-wifi":
        raise ValueError("unreviewed board")
    fields = {
        "socket": ("AM5", "Socket AM5"),
        "chipset": ("B650", "Chipset\nAMD B650"),
        "memory_generation": ("DDR5", "2x DDR5"),
        "memory_speed_max_mts": (7200, "Memory Support DDR5 7200+(OC)"),
        "form_factor": ("itx", "PCB Info\nMini-ITX"),
        "m2_slots": (2, "Storage\n2x M.2"),
    }
    required = ["MPG B650I EDGE WIFI"] + [quote for _, quote in fields.values()]
    if required[0] not in text.splitlines() or any(block not in text for block in required):
        raise ValueError("reviewed official identity/specification changed")
    return required, fields


def verify_original_detail():
    original = ROOT / "scripts/data/catalog-candidates/2026-09-13/offers.jsonl"
    if digest(original) != "1152debccc4a319932d9a9971493531bb5d5b89b9f9e895fb028f4160567b0b6":
        raise ValueError("original offer snapshot changed")
    offers = [o for o in read_rows("offers.jsonl") if o["row_key"] == ROW_KEY and o["model_id"] == SKU]
    if len(offers) != 1:
        raise ValueError("reviewed offer ambiguous or missing")
    offer = offers[0]
    folder = ROOT / "var/data/collections/2026-09-13-maishou-expansion-01"
    rows = [json.loads(line) for line in (folder / "raw_index.jsonl").read_text(encoding="utf-8").splitlines()]
    matches = [r for r in rows if r.get("detail_id") == SKU + "__" + ROW_KEY]
    if len(matches) != 1:
        raise ValueError("original detail ambiguous or missing")
    item = matches[0]
    path = folder / item["file"]
    if not item["ok"] or item["goodsId"] != offer["goodsId"] or str(item["source"]) != offer["platform_src"] or digest(path) != item["sha256"]:
        raise ValueError("original detail identity/hash changed")
    detail = yaml.safe_load(path.read_text(encoding="utf-8"))
    body = detail["商品详情"]
    if str(body["platformId"]) != offer["platform_src"] or detail["商品标题"] != offer["title"] or detail["购买链接"] != offer["buy_url"] or body["shopName"] != offer["shop"] or Decimal(str(body["actualPrice"])) != Decimal("1450") or Decimal(str(offer["price_cny"])) != Decimal("1450"):
        raise ValueError("reviewed title/seller/link/price mismatch")
    return {"file": path.relative_to(ROOT).as_posix(), "sha256": item["sha256"], "observed_at": item["ts"],
            "request_goods_id": item["goodsId"], "response_goods_id": body["goodsId"], "offer": offer,
            "original_offer_file_sha256": digest(original),
            "note": "Opaque goodsId values differ; original request index, exact title, seller, purchase link and price bind this saved observation. No claim of fresh price or stock."}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--pages", required=True, type=Path)
    parser.add_argument("--out", required=True, type=Path)
    args = parser.parse_args()
    detail = verify_original_detail()
    args.out.mkdir(parents=True, exist_ok=False)
    # Other reviewed models can share one aggregation row. Preserve that raw
    # file and pass only this exact reviewed model/row into the common builder.
    source = args.out / "reviewed-offers.jsonl"
    jsonl(source, [{**detail["offer"], "original_offer_file_sha256": detail["original_offer_file_sha256"],
                    "detail_source_file": detail["file"], "detail_source_sha256": detail["sha256"]}])
    prepare(args.out, args.pages, choices=CHOICES, check=check_spec, category="motherboard", offer_source=source, offer_sha256=digest(source))
    (args.out / "detail-review.json").write_bytes((json.dumps(detail, ensure_ascii=False, indent=2) + "\n").encode("utf-8"))


if __name__ == "__main__":
    main()
