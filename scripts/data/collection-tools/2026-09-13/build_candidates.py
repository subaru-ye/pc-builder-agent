# -*- coding: utf-8 -*-
"""把 raw_index 里成功的搜索行汇成候选池(pool_for_review.jsonl),供复核分流。

- 按 (source, goodsId) 去重,保留全部来源查询;同价同题多 goodsId 标 dup_title_price。
- 用 legacy_patterns 分流:命中旧 160 身份 → legacy;否则 → new(新候选池)。
- 垃圾词(二手/整机/套装等)只标 flag,不删除——复核保留拒绝理由。

用法:python build_candidates.py [--raw-dir 覆盖默认目录]
输出:<raw>/pool_for_review.jsonl
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
    HERE.parents[3] / "var/data/collections/2026-09-13-maishou-expansion-01"
)

JUNK = ["拆机", "二手", "准新", "坏", "维修", "回收", "出租", "样品", "询价", "议价", "成新", "矿卡", "整机", "板u", "主板cpu套装"]


def sq(s: str) -> str:
    s = str(s or "").lower()
    s = re.sub(r"g\s*[x×*]\s*(\d)", r"g\1", s)
    return re.sub(r"[^a-z0-9\u4e00-\u9fff]", "", s)


def main() -> int:
    plan = {
        q["query_id"]: q
        for q in (json.loads(l) for l in (HERE / "search_plan.jsonl").read_text(encoding="utf-8").splitlines() if l.strip())
    }
    index = [json.loads(l) for l in (RAW / "raw_index.jsonl").read_text(encoding="utf-8").splitlines() if l.strip()]
    seen: dict[tuple, int] = {}
    pool = []
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
            seen[key] = len(pool)
            if legacy_of:
                legacy_rows.append(row)
            else:
                pool.append(row)
    out_new = RAW / "pool_for_review.jsonl"
    out_legacy = RAW / "legacy_price_hits_pool.jsonl"
    out_new.write_text(
        "\n".join(json.dumps(o, ensure_ascii=False) for o in pool) + "\n", encoding="utf-8"
    )
    out_legacy.write_text(
        "\n".join(json.dumps(o, ensure_ascii=False) for o in legacy_rows) + "\n", encoding="utf-8"
    )
    from collections import Counter

    print(json.dumps({
        "unique_rows": len(pool) + len(legacy_rows),
        "new_pool": len(pool),
        "legacy_pool": len(legacy_rows),
        "new_by_cat": dict(Counter(o["category"] for o in pool)),
        "legacy_by_cat": dict(Counter(o["category"] for o in legacy_rows)),
    }, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    sys.exit(main())
