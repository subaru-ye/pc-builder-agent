"""Freeze an evidence-based disposition for every remaining candidate.

Disposition means whether current evidence permits publication, not an assertion
that the underlying product does not exist. Original captures stay untouched.
"""
import hashlib
import json
from collections import Counter
from pathlib import Path

from audit_candidates import ROOT, SOURCE, audit

def lines(path):
    return [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]

def main():
    candidates, offers = SOURCE / "candidates.jsonl", SOURCE / "offers.jsonl"
    audit_rows = audit(lines(candidates), lines(offers))
    published = set()
    inputs = [candidates, offers]
    for batch in ("2026-09-14-amd", "2026-09-14-ssd", "2026-09-15-gpu", "2026-09-15-memory-case"):
        p = ROOT / "scripts/data/reviewed-intake" / batch / "offers.json"
        inputs.append(p)
        published.update(row["original_model_id"] for row in json.loads(p.read_text(encoding="utf-8")))
    decisions = []
    for row in audit_rows:
        mid = row["model_id"]
        if mid in published:
            continue
        primary = row["primary_offer"]
        conflict = row["identity_status"] == "conflict"
        missing = row["missing_official_evidence_fields"]
        reasons = list(row["issues"])
        reasons.append("现有记录尚未提供对应精确型号的官方字段证据：" + "、".join(missing))
        if not primary.get("buy_url"):
            reasons.append("原主报价没有详情购买链接，需要核对选中变体和当前观察价格；不得补造链接")
        decisions.append({
            "model_id": mid, "category": row["category"], "name": row["name"],
            "disposition": "pending_evidence", "publication": "withheld",
            "primary_offer_binding": "rejected" if conflict else "needs_exact_variant_review",
            "reason": reasons, "official_fields_required": missing,
            "missing_title_fields": row["missing_compatibility_fields"],
            "primary_row_key": primary["row_key"], "primary_title": primary["title"],
            "primary_price_cny": primary["price_cny"], "primary_buy_url": primary.get("buy_url"),
            "priority": "whole_build_coverage" if row["category"] in ("gpu", "memory", "ssd", "case") else "platform_or_compatibility",
            "next_action": "先找精确变体报价，再补官网规格" if conflict else "核对精确变体与官方字段；报价继续走买手复核",
        })
    assert len(published) == 14 and len(decisions) == 101
    assert len({r["model_id"] for r in decisions}) == 101
    raw = b"".join((json.dumps(row, ensure_ascii=False, sort_keys=True) + "\n").encode("utf-8") for row in decisions)
    out = ROOT / "scripts/data/reviewed-intake/2026-09-15-disposition"
    out.mkdir(exist_ok=True)
    target = out / "decisions.jsonl"
    if target.exists() and target.read_bytes() != raw:
        raise ValueError("frozen dispositions differ; create a new reviewed batch")
    target.write_bytes(raw)
    manifest = {
        "schema_version": 1, "reviewed_by": "Agent依据已保存采集与字段证据审查",
        "remaining_candidates": len(decisions), "pending_evidence": len(decisions),
        "rejected_primary_bindings": sum(r["primary_offer_binding"] == "rejected" for r in decisions),
        "missing_original_buy_links": sum(not r["primary_buy_url"] for r in decisions),
        "category_counts": dict(Counter(r["category"] for r in decisions)),
        "decisions_sha256": hashlib.sha256(raw).hexdigest(),
        "inputs": {p.relative_to(ROOT).as_posix(): hashlib.sha256(p.read_bytes()).hexdigest() for p in inputs},
        "new_network_requests": 0, "database_modified": False,
        "meaning": "全部暂缓发布，各自列出缺项；拒绝的是无法绑定的当前报价，不是断言该型号不存在。后续补证据后新建审查批次，不改本次记录。",
    }
    (out / "manifest.json").write_bytes((json.dumps(manifest, ensure_ascii=False, indent=2) + "\n").encode("utf-8"))
    print(json.dumps({k: manifest[k] for k in ("remaining_candidates", "pending_evidence", "rejected_primary_bindings", "missing_original_buy_links")}, ensure_ascii=False))

if __name__ == "__main__":
    main()
