"""Offline comparison of saved responses; no model, search, or page requests.

Scores are source-retention checks reviewed against adjacent labels/conditions.
They do not establish hardware truth or downstream answer accuracy.
"""
import csv
import hashlib
import json
import statistics
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
OUT = ROOT / "artifacts/firecrawl-comparison-20260914"
DATASET = ROOT / "scripts/eval/page-reader/dataset.json"
METHODS = ("http", "browser", "firecrawl")
COMMON = ("kingston", "amd", "zol", "msi-spec", "noctua")
MEANINGFUL = {
    "http": {"amd", "zol"},
    "browser": set(COMMON) | {"gigabyte", "smzdm"},
    "firecrawl": set(COMMON) | {"gigabyte", "smzdm"},
}
NOTES = {
    "kingston": "HTTP403; both browser readers retain four points but also much unrelated content.",
    "amd": "All retain five labeled facts. Plain HTTP is shorter.",
    "zol": "HTTP now decodes Chinese correctly. Crawl4AI facts start after 26k chars; Firecrawl removes preceding navigation.",
    "asus": "All unavailable this run: HTTP404, browser failure, Firecrawl HTTP502 maintenance page. No retries.",
    "msi-spec": "Both browser readers retain the four qualified facts, including ECC mode and M.2/PCIe restriction.",
    "msi-support": "HTTP403; Crawl4AI rejects cookie-only content; Firecrawl returns support shell. Neither supplies 5900X BIOS row.",
    "noctua": "Both retain five source points including 168mm/160mm and weights; numbers are source claims, not independent verification.",
    "gigabyte": "Both return Rev1.0/product information; neither contains target 5900X/minimum BIOS row.",
    "crucial": "HTTP200 Request Rejected is incorrectly accepted; Crawl4AI errors; Firecrawl HTTP404 with license/navigation, no guide.",
    "smzdm": "Both browser readers retain dated 2023-02-20 deal; not current quote. Recommendations remain.",
    "jd": "HTTP shell; browser failure; Firecrawl HTTP200 returns only a dot. Not product evidence.",
    "taobao": "HTTP shell; browser failure; Firecrawl HTTP200 returns only X. Not product evidence.",
}

def read(path):
    return json.loads(path.read_text(encoding="utf-8"))

def write(path, data):
    path.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

def normalize(text):
    return text.replace("\\_", "_").lower()

def grades(body, page):
    if page.get("exclude") or not page["positive"]:
        return None
    # Each conjunction was manually inspected in contexts.txt for adjacent labels,
    # negation and applicability, not just whole-document coincidental matches.
    lower = normalize(body)
    return [int(all(term.lower() in lower for term in point[1:])) for point in page["points"]]

def median(values):
    return round(statistics.median(values), 3) if values else None

pages = read(DATASET)
rows, texts, point_evidence = [], {}, {}
for page in pages:
    for method in METHODS:
        path = OUT / (f"firecrawl/first-{page['id']}.json" if method == "firecrawl" else f"baseline/{page['id']}-{method}.json")
        raw = read(path)
        if method == "firecrawl":
            body = raw.get("data", {}).get("markdown", "")
            metadata = raw.get("data", {}).get("metadata", {})
            status, elapsed = metadata.get("statusCode"), raw["elapsed_ms"]
            error, credits = raw.get("is_error", False), metadata.get("creditsUsed")
            final_url = metadata.get("url") or metadata.get("sourceURL")
        else:
            source = raw.get("page") or {}
            body, status = source.get("text", ""), source.get("http_status")
            elapsed, error, credits = raw["duration_ms"], bool(raw["error"]), None
            final_url = source.get("final_url")
            if not status and raw.get("attempts"):
                status = raw["attempts"][-1].get("status")
        key = page["id"] + "-" + method
        texts[key + "/full"] = body
        texts[key + "/head16000"] = body[:16000]
        eligible = not error and status == 200
        full = grades(body if eligible else "", page)
        head = grades(body[:16000] if eligible else "", page)
        point_evidence[key] = {"full": full, "head16000": head, "note": NOTES[page["id"]]}
        rows.append({
            "id": page["id"], "method": method, "seconds": elapsed/1000,
            "status": status, "tool_error": error, "meaningful_body": page["id"] in MEANINGFUL[method],
            "chars_full": len(body), "chars_head16000": len(body[:16000]),
            "retained_full": sum(full) if full is not None else None,
            "retained_head16000": sum(head) if head is not None else None,
            "reference_points": len(full) if full is not None else None,
            "credits_reported": credits, "final_url": final_url,
        })
write(OUT / "texts-for-tokenizer.json", texts)
write(OUT / "point-review.json", point_evidence)

token_path = OUT / "tokens.json"
if token_path.exists():
    tokens = read(token_path)
    for row in rows:
        key = row["id"] + "-" + row["method"]
        for field in ["full", "head16000"]:
            row["tokens_" + field] = tokens["counts"][key + "/" + field]["o200k_base"]

summary = {}
for method in METHODS:
    group = [r for r in rows if r["method"] == method]
    meaningful = [r for r in group if r["meaningful_body"]]
    common = [r for r in group if r["id"] in COMMON]
    summary[method] = {
        "requests": len(group),
        "meaningful_bodies": len(meaningful),
        "seconds_all_median": median([r["seconds"] for r in group]),
        "seconds_all_max": max(r["seconds"] for r in group),
        "seconds_all_total": round(sum(r["seconds"] for r in group), 3),
        "seconds_meaningful_median": median([r["seconds"] for r in meaningful]),
        "seconds_common_five_median": median([r["seconds"] for r in common]) if method != "http" else None,
        "source_points_full": sum(r["retained_full"] or 0 for r in group),
        "source_points_head16000": sum(r["retained_head16000"] or 0 for r in group),
        "reference_points": 27,
        "returned_chars_full": sum(r["chars_full"] for r in group),
    }
    if token_path.exists():
        summary[method]["common_five_tokens_full"] = sum(r["tokens_full"] for r in common)
        summary[method]["common_five_tokens_head16000"] = sum(r["tokens_head16000"] for r in common)
        summary[method]["common_five_note"] = "HTTP contains failed empty responses; do not compare its aggregate as cheaper equivalent coverage."
searches = [read(p) for p in sorted((OUT / "firecrawl").glob("search-*.json"))]
repeats = [read(p) for p in sorted((OUT / "firecrawl").glob("repeat-*.json"))]
summary["firecrawl"]["repeat_results"] = [
    {"id": r["id"], "seconds": r["elapsed_ms"]/1000,
     "same_markdown_as_first": r["data"].get("markdown") == read(OUT / f"firecrawl/first-{r['id']}.json")["data"].get("markdown"),
     "credits": r["data"].get("metadata", {}).get("creditsUsed")} for r in repeats
]
summary["usage"] = {
    "http_reads": 12, "crawl4ai_reads": 12,
    "firecrawl_first_reads": 12, "firecrawl_repeat_reads": len(repeats),
    "firecrawl_searches": len(searches), "automatic_retries": 0,
    "firecrawl_credits_reported": sum(r["credits_reported"] or 0 for r in rows if r["method"] == "firecrawl")
      + sum(r["data"]["metadata"]["creditsUsed"] for r in repeats)
      + sum(r["data"]["creditsUsed"] for r in searches),
    "model_api_requests": 0, "embedding_api_requests": 0, "serpapi_requests": 0,
    "note": "Provider-internal work/retries unobservable. Prior-turn 4-credit probe excluded.",
}
write(OUT / "summary.json", summary)
write(OUT / "rows.json", rows)
with (OUT / "rows.csv").open("w", encoding="utf-8", newline="") as f:
    writer = csv.DictWriter(f, fieldnames=list(rows[0]))
    writer.writeheader()
    writer.writerows(rows)
print(json.dumps(summary, ensure_ascii=False, indent=2))
