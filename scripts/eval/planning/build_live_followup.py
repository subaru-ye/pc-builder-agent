"""Predeclare a new bounded batch; retain the first batch and its failed grades."""
import copy
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]


def main():
    source = ROOT / "internal/planningeval/testdata/live-smoke-20260915/suite.json"
    raw = source.read_bytes()
    pin = json.loads(source.with_name("provenance.json").read_bytes())
    if hashlib.sha256(raw).hexdigest() != pin["suite_sha256"]:
        raise ValueError("Original frozen suite changed")
    suite = json.loads(raw)
    suite["version"] = "live-followup-20260915-v1"
    suite["provenance"] = "第二批：预算交付修复、必要补充后结束追问、CPU升级；完整174件冻结目录，真实模型、离线网页"
    state_case, build_case = suite["cases"]
    state_case["id"] = "FU-001"
    state_case["title"] = "补充分辨率后进入需求确认，不再追问可选偏好"
    state_case["steps"][2]["expect"]["next_action"] = "confirm"
    build_case["id"] = "FU-002"
    build_case["title"] = "正式交付后直接检索升级CPU，优先保留其他配件"
    upgrade = {
        "kind": "message",
        "text": "把处理器换更好的，预算还很充足啊，其他配件尽量不动。直接检索合适的处理器并更新方案。",
        "expect": {
            "versions": 2, "outcome": "ready", "validation": "pass",
            "missing_prices": 0, "budget_ceiling_cny": "8000",
            "next_action": "plan", "cpu_changed": True, "preserve_other_parts": True,
            "require_tools": ["search_local", "evaluate"],
            "forbid_tools": ["search_web", "read_page"],
            "fields": {
                "budget_cny": {"value": 8000, "status": "active"},
                "priority": {"value": ["cpu"], "status": "active", "strength": "prefer"},
                "use_case.type": {"value": "gaming", "status": "active"},
                "use_case.resolution": {"value": "2K", "status": "active"},
                "owned_parts": {"status": "unknown"},
            },
        },
    }
    build_case["steps"].insert(2, upgrade)
    build_case["steps"][-1]["expect"]["versions"] = 2
    retry = copy.deepcopy(build_case["steps"][-1])
    retry["kind"] = "retry"
    build_case["steps"].append(retry)
    out = ROOT / "internal/planningeval/testdata/live-followup-20260915"
    out.mkdir(exist_ok=False)
    data = (json.dumps(suite, ensure_ascii=False, indent=2) + "\n").encode("utf-8")
    provenance = {
        "source_suite_sha256": pin["suite_sha256"],
        "suite_sha256": hashlib.sha256(data).hexdigest(),
        "max_model_requests": 24, "external_requests": 0, "embedding_requests": 0,
        "comparison": "Separate targeted batch, not a rerun of SM-001 under unchanged scoring",
        "upgrade_grading": "CPU identity change verifies execution only. Preserve-other-parts is a targeted feasibility check; any necessary linkage must be reviewed, never silently counted as a pass.",
    }
    (out / "suite.json").write_bytes(data)
    (out / "provenance.json").write_bytes((json.dumps(provenance, ensure_ascii=False, indent=2) + "\n").encode("utf-8"))


if __name__ == "__main__":
    main()
