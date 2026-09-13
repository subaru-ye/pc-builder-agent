# -*- coding: utf-8 -*-
"""按 new_models.py 的身份表在候选池里逐型号匹配 offer,产出候选草案。

- 在 new 池与 legacy 池中同时匹配(legacy 池里可能是被"宁多标"误分流的行)。
- 带 junk_words 标记的行不作为 offer 证据。
- 价格窗仅挡配件噪声;越窗行丢弃并计数,便于修正正则。
- 输出 <raw>/candidates_draft.jsonl + 控制台摘要。
"""
from __future__ import annotations

import json
import re
import statistics
import sys
from pathlib import Path

from build_candidates import RAW, sq
from new_models import CAT_DEFAULTS, MODELS

# 品类级排除词:配件/水冷头/整机搭售等非商品行(与 JUNK 互补)
GLOBAL_NOT = {
    "cpu": [r"主板|显卡|套装|整机|主机|组装|装机|笔记本|风扇|硬盘|水冷"],
    "gpu": [r"水冷|冷头|分体|alphacool|欧酷|bykski|挡板|挡片|支架|延长|竖装|背板|套装|电源"],
    "motherboard": [r"挡板|挡片|套装|套餐|内存主板|搭|固态|水冷"],
    "memory": [r"笔记本|内存卡|tf卡|u盘|固态|散热马甲条$"],
    "ssd": [r"移动固态|硬盘盒|机械硬盘|内存卡|笔记本硬盘"],
    "psu": [r"模组线|线材|定制线"],
    "case": [r"防尘网|网罩|风扇|螺丝|支架|配件|挡板|磁吸"],
    "cooler": [r"扣具|支架|螺丝|挡板|垫片|转接"],
}
SPEC_TOKENS = {
    "psu": ["500w", "550w", "600w", "650w", "750w", "850w", "1000w"],
    "ssd": ["500g", "512g", "1tb", "2tb", "4tb"],
    "memory": ["8g2", "16g2", "24g2", "32g2", "48g2", "64g2"],
}


def load_pool(path: Path) -> list[dict]:
    if not path.exists():
        return []
    return [json.loads(l) for l in path.read_text(encoding="utf-8").splitlines() if l.strip()]


def spec_ambiguity(cat: str, s: str) -> list[str]:
    hits = [t for t in SPEC_TOKENS.get(cat, []) if re.search(t, s)]
    if len(hits) >= 2:
        return [f"multi_spec:{'+'.join(hits)}"]
    return []


CPU_TOKEN = re.compile(r"(?:i[3579]|r[3579]|ultra\s*[579]|锐龙\s*[3579])\s?\d{4,5}[a-z]{0,3}")
CPU_TOKEN_SQ = re.compile(r"(?:i[3579]|r[3579]|ultra[579]|锐龙[3579])\d{4,5}[a-z]{0,3}")
GPU_TOKEN_SQ = re.compile(r"(?:rtx?|rx|gtx|arc)?\d{3,4}(?:ti|xt|xtx|super)?|(?:a|b)\d{3}")


def model_bound(cat: str, token_sq: str, s_sq: str) -> bool:
    """多型号行只有在"尾段/【】内出现目标型号"时才视为价签绑定。"""
    tok_re = CPU_TOKEN_SQ if cat == "cpu" else GPU_TOKEN_SQ
    toks = {m.group(0) for m in tok_re.finditer(s_sq)}
    if len(toks) <= 1:
        return True
    for m in re.finditer(r"【([^】]*)】", s_sq):
        if token_sq in m.group(1):
            return True
    tail = s_sq[-16:]
    return token_sq in tail


def match_model(m: dict, rows: list[dict], lo: float, hi: float, relaxed: bool) -> tuple[list[dict], int]:
    matched, n_junk, n_unbound = [], 0, 0
    for r in rows:
        s = sq(r["title"])
        if "junk_words" in r.get("flags", []):
            n_junk += 1
            continue
        if not all(re.search(p, s) for p in m["must"]):
            continue
        nots = list(m.get("not_", []))
        if not relaxed:
            nots += GLOBAL_NOT.get(m["cat"], [])
            if m["cat"] in ("cpu", "gpu") and m.get("bind") and not model_bound(m["cat"], m["bind"], s):
                n_unbound += 1
                continue
        if any(re.search(p, s) for p in nots):
            continue
        price = r.get("price_cny")
        if price is None or not (lo <= price <= hi):
            continue
        matched.append(r)
    match_model.last_unbound = n_unbound
    return matched, n_junk


def main() -> int:
    pool = load_pool(RAW / "pool_for_review.jsonl") + load_pool(RAW / "legacy_price_hits_pool.jsonl")
    by_cat: dict[str, list[dict]] = {}
    for r in pool:
        by_cat.setdefault(r["category"], []).append(r)

    out = []
    for m in MODELS:
        lo, hi = CAT_DEFAULTS[m["cat"]]
        rows = by_cat.get(m["cat"], [])
        matched, n_junk = match_model(m, rows, lo, hi, relaxed=False)
        relaxed = False
        if not matched and m["cat"] != "gpu":
            matched, n_junk2 = match_model(m, rows, lo, hi, relaxed=True)
            n_junk += n_junk2
            relaxed = True
        # 去重同价同题(跨查询重复)
        seen_tp, offers = set(), []
        for r in sorted(matched, key=lambda x: (x["price_cny"], x["row_key"])):
            tp = (r["platform_src"], r["price_cny"], sq(r["title"]))
            if tp in seen_tp:
                continue
            seen_tp.add(tp)
            offers.append(r)
        prices = [o["price_cny"] for o in offers]
        flags = []
        if relaxed:
            flags.append("relaxed_match")
        if offers:
            flags += spec_ambiguity(m["cat"], sq(offers[0]["title"]))
            med = statistics.median(prices)
            if len(prices) >= 3 and offers[0]["price_cny"] < 0.45 * med:
                flags.append("price_anomaly_low")
            if len(prices) >= 3 and offers[0]["price_cny"] > 2.2 * med:
                flags.append("price_anomaly_high")
            shops = len({o["shop"] for o in offers})
            if shops == 1:
                flags.append("single_shop")
        else:
            flags.append("no_match")
        rec = {
            "model_id": m["id"],
            "category": m["cat"],
            "name": m["name"],
            "n_offers": len(offers),
            "n_junk_excluded": n_junk,
            "relaxed": relaxed,
            "min_price": min(prices) if prices else None,
            "median_price": statistics.median(prices) if prices else None,
            "n_shops": len({o["shop"] for o in offers}) if offers else 0,
            "flags": flags,
            "offers": [
                {
                    "platform_src": o["platform_src"],
                    "goodsId": o["goodsId"],
                    "title": o["title"],
                    "price_cny": o["price_cny"],
                    "original_cny": o["original_cny"],
                    "coupon_cny": o["coupon_cny"],
                    "shop": o["shop"],
                    "month_sales": o["month_sales"],
                    "row_key": o["row_key"],
                    "first_query": o["first_query"],
                    "legacy_of": o["legacy_of"],
                }
                for o in offers[:6]
            ],
        }
        out.append(rec)

    (RAW / "candidates_draft.jsonl").write_text(
        "\n".join(json.dumps(o, ensure_ascii=False) for o in out) + "\n", encoding="utf-8"
    )

    # 复核文件:全标题,按品类分组
    lines = ["# 候选复核 2026-09-13 买手扩库 batch-01", ""]
    for cat in ["cpu", "gpu", "motherboard", "memory", "ssd", "psu", "case", "cooler"]:
        lines.append(f"## {cat}")
        lines.append("")
        for r in out:
            if r["category"] != cat:
                continue
            fl = " ".join(r["flags"]) or "-"
            lines.append(f"### {r['model_id']} | {r['name']} | n={r['n_offers']} min={r['min_price']} | {fl}")
            for o in r["offers"][:3]:
                lines.append(f"- {o['price_cny']} [{o['platform_src']}] {o['title']}  ({o['shop']})")
            lines.append("")
    (RAW / "review_draft.md").write_text("\n".join(lines), encoding="utf-8")

    n_ok = sum(1 for r in out if r["n_offers"] > 0)
    no_match = [r["model_id"] for r in out if r["n_offers"] == 0]
    print(f"models={len(out)} with_offers={n_ok} no_match={len(no_match)}")
    print("NO_MATCH:", ", ".join(no_match))
    return 0


if __name__ == "__main__":
    sys.exit(main())
