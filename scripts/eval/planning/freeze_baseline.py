"""Pin historical evidence and current code; never re-run or rewrite old reports."""
import hashlib
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[3]
HISTORY = ROOT / "artifacts/eval/20260909-134447-3770035618"
OUT = ROOT / "docs/eval/planning-v2/baseline-20260915.json"

def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()

def main():
    meta = json.loads((HISTORY / "meta.json").read_text(encoding="utf-8"))
    rows = [json.loads(line) for line in (HISTORY / "results.jsonl").read_text(encoding="utf-8").splitlines()]
    current = json.loads((ROOT / "var/run/goal-baseline-20260915/models.json").read_text(encoding="utf-8"))
    old_models = meta["configured_models"]
    for role in current:
        current[role]["model_chain"] = current[role].get("model_chain") or []
    assert current == old_models, "current model settings differ: register a separate model comparison"
    assert len(rows) == 150 and len({r["case_id"] for r in rows}) == 50
    assert all(r["verdict"]["passed"] for r in rows)
    assert all({r["seed"] for r in rows if r["case_id"] == mid} == {1, 2, 3} for mid in {r["case_id"] for r in rows})
    sums = {key: sum(r["usage"][key] for r in rows) for key in ("model_calls", "embedding_calls", "usage_responses", "input_tokens", "output_tokens", "total_tokens")}
    data = {
        "schema_version": 1,
        "historical_run": HISTORY.relative_to(ROOT).as_posix(),
        "files": {name: digest(HISTORY / name) for name in ("meta.json", "cases.json", "results.jsonl")},
        "suite_sha256": meta["suite_sha256"],
        "historical_code": meta["code"],
        "current_code": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
        "models": current,
        "historical_passed": 150,
        "historical_cases": 50,
        "repeats": 3,
        "historical_usage": sums,
        "summed_case_duration_ms": sum(r["duration_ms"] for r in rows),
        "limitations": [
            "Old executor and scoring are historical; 150/150 is not a new planning score.",
            "Usage counts logical model calls; historical HTTP retries and embedding tokens were not recorded.",
            "Summed case duration is not process wall-clock time or a causal performance comparison.",
            "New data effects and changed execution/scoring must be reported separately.",
        ],
    }
    raw = (json.dumps(data, ensure_ascii=False, indent=2) + "\n").encode("utf-8")
    if OUT.exists() and OUT.read_bytes() != raw:
        raise ValueError("baseline already frozen; use a separately named revision, never overwrite")
    OUT.write_bytes(raw)
    print(json.dumps({"file": OUT.relative_to(ROOT).as_posix(), "historical_usage": sums, "models_match": True}, ensure_ascii=False))

if __name__ == "__main__":
    main()
