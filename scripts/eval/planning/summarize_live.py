"""Read an in-progress journal or final report without triggering any execution."""
import argparse
import json
from pathlib import Path

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("directory", type=Path)
    args = parser.parse_args()
    path = args.directory / "events.jsonl"
    rows = []
    if path.exists():
        for line in path.read_text(encoding="utf-8").splitlines():
            try:
                rows.append(json.loads(line))
            except json.JSONDecodeError:
                print("Journal ends with an incomplete line; do not restart or reset the budget.")
                break
    print(json.dumps({"request_markers": sum(r["event"] == "model_request" for r in rows), "response_markers": sum(r["event"] == "model_response" for r in rows), "finished_steps": sum(r["event"] == "step_completed" for r in rows)}, ensure_ascii=False))
    report_path = args.directory / "report.json"
    if not report_path.exists():
        print("No final report yet; journal is not a process-liveness signal.")
        return
    report = json.loads(report_path.read_text(encoding="utf-8"))
    print(json.dumps({k: report.get(k) for k in ("mode", "passed", "actual_model_requests", "tokens", "duration_ms", "classifications")}, ensure_ascii=False))
    for case in report["cases"]:
        for i, step in enumerate(case["steps"], 1):
            failures = [{"name": c["name"], "detail": c.get("detail", "")[:300]} for c in step["checks"] if not c["pass"]]
            print(json.dumps({"case": case["id"], "step": i, "class": step["classification"], "failures": failures, "reply": step["reply"][:600], "versions": step["versions"]}, ensure_ascii=False))

if __name__ == "__main__":
    main()
