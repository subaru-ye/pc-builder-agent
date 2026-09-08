from batch_paths import DATA, skill_dir
import json
import re
import subprocess
import sys
import time
from pathlib import Path

SKILL = skill_dir()
REFS = DATA / "accept_refs_20260908.json"
OUT = DATA / "accept_links_20260908.json"

SRC = {"京东": 2, "淘宝/天猫": 1, "拼多多": 3}

def fetch(src, gid):
    r = subprocess.run(
        ["uv", "run", "scripts/main.py", "detail", f"--source={src}", f"--id={gid}"],
        cwd=SKILL, capture_output=True, text=True, encoding="utf-8", errors="replace", timeout=60,
    )
    return r.stdout + "\n" + r.stderr

def parse(text):
    url = tok = price = None
    for line in text.splitlines():
        m = re.match(r"购买链接:\s*(\S+)", line.strip())
        if m:
            url = m.group(1)
        m = re.match(r"复制口令:\s*(.+)", line.strip())
        if m:
            tok = m.group(1).strip()
        m = re.match(r"actualPrice:\s*'([^']+)'", line.strip())
        if m:
            price = m.group(1)
    return url, tok, price

def main():
    accepts = json.loads(REFS.read_text(encoding="utf-8"))
    out = {}
    if OUT.exists():
        out = json.loads(OUT.read_text(encoding="utf-8"))
    for i, a in enumerate(accepts, 1):
        sku = a["sku"]
        if sku in out and out[sku].get("url"):
            continue
        src = SRC.get(a["platform"])
        if src is None:
            out[sku] = {"url": None, "error": f"unknown platform {a['platform']}"}
            continue
        try:
            text = fetch(src, a["goodsId"])
            url, tok, price = parse(text)
            out[sku] = {
                "url": url, "kouling": tok, "detail_price": price,
                "expect_price": a.get("price"), "seller": a.get("seller"),
                "error": None if url else "no link parsed",
            }
        except Exception as e:
            out[sku] = {"url": None, "error": repr(e)}
        OUT.write_text(json.dumps(out, ensure_ascii=False, indent=1), encoding="utf-8")
        ok = "OK " if out[sku].get("url") else "FAIL"
        print(f"[{i}/{len(accepts)}] {ok} {sku} {out[sku].get('url') or out[sku].get('error')}", flush=True)
        time.sleep(0.6)
    done = sum(1 for v in out.values() if v.get("url"))
    print(f"done: {done}/{len(accepts)} links fetched")

if __name__ == "__main__":
    sys.exit(main())
