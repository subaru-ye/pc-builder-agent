# -*- coding: utf-8 -*-
"""2026-09-14 处置批:用户裁定(2026-09-14 会话)执行两项。

① cpu-r7-7700 接受京东【全新散片】1226(r2 review 0.51 人工批准,沿用仅散片判例);
② 其余仍挂 2026-07-28 的 51 个 SKU 全部剔除现价行(SKU 保留在产品目录,仅无现价,
   报价缺失)。决策留痕:7700 标 decision=accept+manual,剔除 SKU 标 final=removed。

用法:python cull_stale_20260914.py [--dry-run]
"""
from __future__ import annotations

import csv
import json
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
DECISIONS = HERE.parents[1] / "catalog-candidates/2026-09-14/legacy-refresh-r2.jsonl"
PRICES = HERE.parents[1] / "prices/2026-09-14.csv"


def main() -> int:
    dry = "--dry-run" in sys.argv
    decs = [json.loads(l) for l in DECISIONS.read_text(encoding="utf-8").splitlines() if l.strip()]
    for d in decs:
        if d["sku"] == "cpu-r7-7700":
            d["decision"] = "accept"
            d["reason"] = "仅散片;用户 2026-09-14 人工批准接受"
            d["manual"] = True
    decs_by_sku = {d["sku"]: d for d in decs}

    rows = list(csv.DictReader(PRICES.read_text(encoding="utf-8").splitlines()))
    # ① 先人工接受 7700(07-28 盒装 → 09-14 散片),再剔除其余 07-28 行
    for r in rows:
        if r["sku"] == "cpu-r7-7700":
            r["price_cny"], r["source"], r["captured_at"] = "1226.00", "maishou88", "2026-09-14"
    kept = []
    removed = []
    for r in rows:
        if r["captured_at"].startswith("2026-07-28"):
            removed.append(r["sku"])
        else:
            kept.append(r)
    for sku in removed:
        if sku in decs_by_sku:
            decs_by_sku[sku]["final"] = "removed_20260914"

    from collections import Counter
    print(f"rows {len(rows)} -> {len(kept)}; removed(0728)={len(removed)}")
    print("removed by cat:", dict(Counter(s.split('-')[0] for s in removed)))
    if dry:
        return 0
    with PRICES.open("w", encoding="utf-8", newline="") as f:
        w = csv.DictWriter(f, fieldnames=["sku", "price_cny", "source", "captured_at"])
        w.writeheader()
        w.writerows(kept)
    DECISIONS.write_text(
        "\n".join(json.dumps(d, ensure_ascii=False) for d in decs) + "\n", encoding="utf-8"
    )
    print(f"written {PRICES} + {DECISIONS}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
