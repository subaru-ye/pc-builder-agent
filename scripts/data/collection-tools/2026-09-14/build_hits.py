# -*- coding: utf-8 -*-
"""2026-09-14 旧 SKU 刷新批次:从原始采集 CSV 提取旧 160 SKU 命中行(legacy hits)。

与 09-13 build_candidates.py 的 legacy 分流同构:LEGACY_PATTERNS 首命中路由,
junk/dup 只作 flag 不丢弃(绑定与守卫由 refresh_legacy_20260914.py 重算)。
用法:python build_hits.py [--raw-dir <dir>]
产出:<raw-dir>/legacy-hits.jsonl
"""
from __future__ import annotations

import csv
import hashlib
import io
import json
import re
import sys
from pathlib import Path

from legacy_patterns import LEGACY_PATTERNS

HERE = Path(__file__).resolve().parent
RAW = Path(sys.argv[sys.argv.index("--raw-dir") + 1]) if "--raw-dir" in sys.argv else (
    HERE.parents[3] / "var/data/collections/2026-09-14-legacy-refresh-01"
)

JUNK = ["拆机", "二手", "准新", "坏", "维修", "回收", "出租", "样品", "询价", "议价", "成新", "矿卡", "整机", "板u", "主板cpu套装", "展机"]


def sq(s: str) -> str:
    s = str(s or "").lower()
    s = re.sub(r"g\s*[x×*]\s*(\d)", r"g\1", s)
    return re.sub(r"[^a-z0-9\u4e00-\u9fff]", "", s)


def main() -> int:
    plans = (
        sys.argv[sys.argv.index("--plan") + 1].split(",")
        if "--plan" in sys.argv
        else ["search_plan.jsonl", "retry_plan.jsonl"]
    )
    plan: dict = {}
    for pf in plans:
        p = HERE / pf
        if p.exists():
            plan.update({
                q["query_id"]: q
                for q in (json.loads(l) for l in p.read_text(encoding="utf-8").splitlines() if l.strip())
            })
    index = [json.loads(l) for l in (RAW / "raw_index.jsonl").read_text(encoding="utf-8").splitlines() if l.strip()]
    seen: dict[tuple, int] = {}
    legacy_rows = []
    title_price_seen: dict[tuple, str] = {}
    for rec in index:
        if rec["kind"] != "search" or not rec["ok"]:
            continue
        q = plan[rec["query_id"]]
        text = (RAW / rec["file"]).read_text(encoding="utf-8")
        rows = list(csv.DictReader(io.StringIO(text)))
        for r in rows:
            title = r.get("title") or ""
            price = float(r["actualPrice"]) if r.get("actualPrice") else None
            key = (r.get("source"), r.get("goodsId"))
            s = sq(title)
            flags = []
            if any(sq(j) in s for j in JUNK):
                flags.append("junk_words")
            legacy_of = None
            for sku, pat in LEGACY_PATTERNS.items():
                if re.search(pat, s):
                    legacy_of = sku
                    break
            if not legacy_of:
                continue
            tp_key = (r.get("source"), price, s)
            dup = title_price_seen.get(tp_key)
            if dup:
                flags.append("dup_title_price")
            else:
                title_price_seen[tp_key] = rec["query_id"]
            row = {
                "row_key": hashlib.sha256(f"{key[0]}|{key[1]}|{s}".encode()).hexdigest()[:16],
                "category": q["category"],
                "query_id": rec["query_id"],
                "keyword": rec["keyword"],
                "page": rec["page"],
                "platform_src": r.get("source"),
                "goodsId": r.get("goodsId"),
                "title": title,
                "price_cny": price,
                "original_cny": float(r["originalPrice"]) if r.get("originalPrice") else None,
                "coupon_cny": float(r["couponPrice"]) if r.get("couponPrice") else None,
                "shop": r.get("shopName"),
                "month_sales": r.get("monthSales"),
                "flags": flags,
                "legacy_of": legacy_of,
                "first_query": dup or rec["query_id"],
            }
            if key in seen:
                continue
            seen[key] = len(legacy_rows)
            legacy_rows.append(row)
    out = RAW / "legacy-hits.jsonl"
    out.write_text("\n".join(json.dumps(r, ensure_ascii=False) for r in legacy_rows) + "\n", encoding="utf-8")
    by_sku = {}
    for r in legacy_rows:
        by_sku[r["legacy_of"]] = by_sku.get(r["legacy_of"], 0) + 1
    print(f"hits={len(legacy_rows)} skus={len(by_sku)} -> {out}")
    for s, n in sorted(by_sku.items(), key=lambda x: -x[1]):
        print(f"  {s:40} {n}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
