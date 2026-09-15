"""Freeze saved live responses for product regressions, never as new live scores."""
import argparse
import hashlib
import json
from pathlib import Path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", required=True)
    parser.add_argument("--out", required=True)
    args = parser.parse_args()
    source, out = Path(args.source), Path(args.out)
    suite_raw, report_raw = (source / "suite.json").read_bytes(), (source / "report.json").read_bytes()
    suite, report = json.loads(suite_raw), json.loads(report_raw)
    if not suite.get("live") or report["mode"] != "live_models_offline_tools":
        raise ValueError("Source must be a completed live run")
    if hashlib.sha256(suite_raw).hexdigest() != report["suite_sha256"]:
        raise ValueError("Saved suite hash differs from the live report")
    records = {case["id"]: case for case in report["cases"]}
    for case in suite["cases"]:
        steps = records[case["id"]]["steps"]
        if len(steps) != len(case["steps"]):
            raise ValueError("Cannot freeze an incomplete case")
        for step, record in zip(case["steps"], steps):
            if step["kind"] != record["kind"] or step.get("text", "") != record.get("text", ""):
                raise ValueError("Source step mismatch")
            for trace in record.get("trace") or []:
                if trace.get("error") or not trace.get("response") or not trace.get("provider_called"):
                    raise ValueError("Missing successful provider response")
                if trace["role"] == "screening":
                    if "screen_oracle" in step:
                        raise ValueError("Multiple Screening responses are not supported")
                    parts = trace["response"]["parts"]
                    text = "".join(p.get("text", "") for p in parts if not p.get("thought"))
                    step["screen_oracle"] = json.loads(text)
                elif trace["role"] == "builder":
                    step.setdefault("builder_oracle", []).append(trace["response"])
                else:
                    raise ValueError("Unknown model role")
    suite["live"] = False
    suite["version"] += "-recorded"
    suite["provenance"] = "保存的真实回答原样回放；验证代码和数据库链路，不计为真实模型复测"
    raw = (json.dumps(suite, ensure_ascii=False, indent=2) + "\n").encode("utf-8")
    provenance = {
        "source": str(source),
        "source_suite_sha256": hashlib.sha256(suite_raw).hexdigest(),
        "source_report_sha256": hashlib.sha256(report_raw).hexdigest(),
        "suite_sha256": hashlib.sha256(raw).hexdigest(),
        "expectations_changed": False,
        "actual_model_requests": 0,
        "external_requests": 0,
    }
    out.mkdir(parents=True, exist_ok=False)
    (out / "suite.json").write_bytes(raw)
    (out / "provenance.json").write_bytes((json.dumps(provenance, ensure_ascii=False, indent=2) + "\n").encode("utf-8"))


if __name__ == "__main__":
    main()
