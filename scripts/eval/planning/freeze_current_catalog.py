"""Freeze the published catalog without rerunning intake or changing history."""
import importlib.util
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[3]
OUT = ROOT / "artifacts/current-123-freeze-20260915"
spec = importlib.util.spec_from_file_location("intake_audit", ROOT / "scripts/data/collection-tools/2026-09-14/audit_candidates.py")
audit = importlib.util.module_from_spec(spec)
spec.loader.exec_module(audit)


def main():
    OUT.mkdir(parents=True, exist_ok=False)
    result = audit.freeze_database(OUT, "pc-builder-agent-postgres-1")
    assert result["latest_id"] == 9
    assert sum(c["active"] for c in result["categories"].values()) == 123
    assert all(c["active"] == c["priced"] for c in result["categories"].values())
    result["parts_release"] = json.loads((ROOT / "var/data/current.json").read_text())["release_id"]
    result["source_commit"] = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    result["model_requests"] = result["external_requests"] = 0
    audit.write_json(OUT / "manifest.json", result)
    print(json.dumps(result))


if __name__ == "__main__":
    main()
