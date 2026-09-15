"""Freeze live smoke expectations before any provider calls; no model answers."""
import hashlib
import json
from pathlib import Path

from build_snapshot_pair import ROOT, catalog, load_pinned

def main():
    source = ROOT / "artifacts/data-intake-after-memory-case-20260915-r1/database-snapshot.json"
    source_hash = "b5d33609c593193f50861de9dc44613d3b0d3a401cab8a0cad94caa7e7c97f79"
    fixture = catalog(load_pinned(source, source_hash), 8)
    fields = {"budget_cny": {"value": 8000, "status": "active"}, "use_case.type": {"value": "gaming", "status": "active"}}
    cases = [
        {"id": "SM-001", "title": "历史L5-301原文：补充分辨率前后保留最新预算", "source": "internal/evalsuite/testdata/cases/L5-301.json；只迁移旧固定追问预期", "steps": [
            {"kind": "message", "text": "预算8000元的新主机，主要玩游戏。", "expect": {"versions": 0, "builder_calls": 0, "fields": {**fields, "use_case.resolution": {"status": "unknown"}}}},
            {"kind": "message", "text": "预算改为6000元，分辨率等下补充。", "expect": {"versions": 0, "builder_calls": 0, "fields": {**fields, "budget_cny": {"value": 6000, "status": "active"}, "use_case.resolution": {"status": "unknown"}}}},
            {"kind": "message", "text": "用2K分辨率。", "expect": {"versions": 0, "builder_calls": 0, "fields": {**fields, "budget_cny": {"value": 6000, "status": "active"}, "use_case.resolution": {"value": "2K", "status": "active"}}}},
            {"kind": "refresh", "expect": {"versions": 0, "builder_calls": 0, "fields": {"budget_cny": {"value": 6000, "status": "active"}}}},
        ]},
        {"id": "SM-002", "title": "8000元2K游戏：真实需求确认及本地目录规划", "source": "历史L1-001场景，显式预算上限；不指定SKU或模型调用次数", "steps": [
            {"kind": "message", "text": "预算最多8000元，全新主机，不含显示器和外设；主要2K玩黑神话悟空，没有已有配件，品牌和机箱尺寸不限。", "expect": {"versions": 0, "builder_calls": 0, "fields": {**fields, "use_case.resolution": {"value": "2K", "status": "active"}}}},
            {"kind": "confirm", "expect": {"versions": 1, "outcome": "ready", "validation": "pass", "missing_prices": 0, "budget_ceiling_cny": "8000", "require_tools": ["search_local", "evaluate"], "forbid_tools": ["search_web", "read_page"], "fields": fields}},
            {"kind": "refresh", "expect": {"versions": 1, "builder_calls": 0, "fields": fields}},
        ]},
    ]
    suite = {"live": True, "version": "live-smoke-20260915-v1", "provenance": "真实模型，完整174件冻结目录；无预置模型回答，网页与搜索仍离线", "catalog": fixture, "pages": {}, "cases": cases}
    raw = (json.dumps(suite, ensure_ascii=False, indent=2) + "\n").encode("utf-8")
    out = ROOT / "internal/planningeval/testdata/live-smoke-20260915"
    out.mkdir(exist_ok=False)
    (out / "suite.json").write_bytes(raw)
    (out / "provenance.json").write_bytes((json.dumps({"suite_sha256": hashlib.sha256(raw).hexdigest(), "snapshot": source.relative_to(ROOT).as_posix(), "snapshot_sha256": source_hash, "price_snapshot_id": 8, "max_model_requests": 12, "external_requests": 0, "embedding_requests": 0}, ensure_ascii=False, indent=2) + "\n").encode("utf-8"))

if __name__ == "__main__":
    main()
