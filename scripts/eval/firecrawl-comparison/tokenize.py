"""Tokenize saved bodies offline using the existing pinned Crawl4AI image."""
import argparse
import json
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
OUT = ROOT / "artifacts/firecrawl-comparison-20260914"
parser = argparse.ArgumentParser()
parser.add_argument("--cache", required=True, help="Existing tiktoken cache directory; network disabled.")
args = parser.parse_args()
cache = Path(args.cache).resolve()
if not cache.is_dir():
    raise SystemExit("Existing vocabulary cache required; this command never downloads it.")
code = """import json,sys,tiktoken,importlib.metadata
texts=json.load(sys.stdin)
encs={n:tiktoken.get_encoding(n) for n in ['o200k_base','cl100k_base']}
print(json.dumps({'tiktoken_version':importlib.metadata.version('tiktoken'),'counts':{k:{n:len(e.encode(v,disallowed_special=())) for n,e in encs.items()} for k,v in texts.items()}}))
"""
result = subprocess.run([
    "docker", "run", "--rm", "-i", "--network", "none",
    "--memory", "1g", "--cpus", "1", "--entrypoint", "python",
    "-e", "TIKTOKEN_CACHE_DIR=/token-cache", "-v", str(cache)+":/token-cache:ro",
    "unclecode/crawl4ai:0.9.3", "-c", code,
], input=(OUT / "texts-for-tokenizer.json").read_text(encoding="utf-8"),
   encoding="utf-8", capture_output=True, check=True)
(OUT / "tokens.json").write_text(result.stdout, encoding="utf-8")
print("Counted saved bodies with network disabled; no model requests.")
