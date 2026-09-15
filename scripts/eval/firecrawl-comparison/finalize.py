"""Archive evaluation provenance and verify the bounded request ledger, offline."""
import hashlib
import json
import shutil
import subprocess
from datetime import datetime, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
OUT = ROOT / "artifacts/firecrawl-comparison-20260914"
source_files = [
    "internal/planning/page_reader.go", "internal/planning/web.go",
    "internal/planning/crawl4ai.go", "internal/planning/runner.go",
    "internal/planning/types.go", "deploy/crawl4ai/reader.py",
    "scripts/eval/page-reader/main.go", "scripts/eval/page-reader/run.py",
    "scripts/eval/page-reader/dataset.json",
]
for name in source_files:
    dest = OUT / "source" / name
    dest.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(ROOT / name, dest)
prior = Path("C:/code/pc-builder-agent-ab-eval/artifacts/crawl4ai-ab-20260914/references")
if prior.is_dir():
    shutil.copytree(prior, OUT / "references", dirs_exist_ok=True)
cache = Path("C:/code/pc-builder-agent-ab-eval/artifacts/crawl4ai-ab-20260914/tokenizer-cache")
ledger = json.loads((OUT / "requests.json").read_text(encoding="utf-8"))
summary = json.loads((OUT / "summary.json").read_text(encoding="utf-8"))
assert len(ledger) == 17
assert sum(r["tool"] == "firecrawl_scrape" for r in ledger) == 14
assert sum(r["tool"] == "firecrawl_search" for r in ledger) == 3
assert sum(r["credits"] for r in ledger) == 20
assert len(list((OUT / "baseline").glob("*-http.json"))) == 12
assert len(list((OUT / "baseline").glob("*-browser.json"))) == 12
assert [summary[m]["source_points_full"] for m in ["http", "browser", "firecrawl"]] == [9, 22, 22]
assert [summary[m]["source_points_head16000"] for m in ["http", "browser", "firecrawl"]] == [9, 18, 22]
assert all(r["request"].get("proxy") == "basic" and r["request"].get("maxAge") == 0 for r in ledger if r["tool"] == "firecrawl_scrape")
sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
tracked = list(OUT.rglob("*"))
manifest = {
    "created_at": datetime.now(timezone.utc).isoformat(),
    "head": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
    "image_id": subprocess.check_output(["docker", "image", "inspect", "--format", "{{.Id}}", "unclecode/crawl4ai:0.9.3"], text=True).strip(),
    "sources": {p: sha(ROOT / p) for p in source_files},
    "tokenizer_cache": {p.name: sha(p) for p in cache.iterdir() if p.is_file()},
    "artifacts": {p.relative_to(OUT).as_posix(): sha(p) for p in tracked if p.is_file() and p.name != "run-manifest.json"},
    "evaluation_files": {p.relative_to(ROOT).as_posix(): sha(p) for p in (ROOT / "scripts/eval/firecrawl-comparison").glob("*.py")},
    "scope": "38 explicit page reads + 3 searches; 20 reported Firecrawl credits; zero model/embedding/SerpAPI requests; provider internal retries unknown.",
    "shared_services_restarted": False,
}
(OUT / "run-manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2)+"\n", encoding="utf-8")
print("Verified 41 explicit requests, 20 reported credits, scores and snapshot hashes.")
