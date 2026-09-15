"""Compare recorded input size; characters are not billed tokens or latency."""
import argparse
import hashlib
import json
from pathlib import Path


def summarize(path):
    raw = path.read_bytes()
    report = json.loads(raw)
    rows = []
    for case in report["cases"]:
        for index, step in enumerate(case["steps"], 1):
            for turn, trace in enumerate(step.get("trace") or [], 1):
                if trace["role"] == "builder":
                    rows.append({"case": case["id"], "step": index, "trace": turn,
                                 "response_sha256": hashlib.sha256(json.dumps(trace["response"], ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode("utf-8")).hexdigest(),
                                 "chars": len(json.dumps(trace["request"], ensure_ascii=False, separators=(",", ":")))})
    return {"report": str(path), "sha256": hashlib.sha256(raw).hexdigest(),
            "mode": report["mode"], "catalog_sha256": report["catalog_sha256"],
            "requests": rows, "total_chars": sum(r["chars"] for r in rows)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("before", type=Path)
    parser.add_argument("after", type=Path)
    args = parser.parse_args()
    before, after = summarize(args.before), summarize(args.after)
    identity = lambda r: (r["case"], r["step"], r["trace"], r["response_sha256"])
    if before["catalog_sha256"] != after["catalog_sha256"] or [identity(r) for r in before["requests"]] != [identity(r) for r in after["requests"]]:
        raise ValueError("Different execution sequences cannot be compared as identical replay")
    print(json.dumps({"before": before, "after": after,
                      "saved_chars": before["total_chars"] - after["total_chars"],
                      "limitation": "Saved original responses replayed against changed code; input character size only, no new model/latency/cost result."}, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
