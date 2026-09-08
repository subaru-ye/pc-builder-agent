# -*- coding: utf-8 -*-
"""Driver: run maishou search for each keyword via subprocess (no shell quoting issues)."""
from batch_paths import DATA, skill_dir
import csv
import io
import json
import subprocess
import time

UV = "uv"
SKILL_DIR = skill_dir()
OUT = DATA / "maishou_results.json"

KEYWORDS = [
    "i3 12100F",
    "i5 12400F",
    "锐龙5 5500",
    "锐龙5 5600",
    "锐龙5 7500F",
    "微星 PRO H610M-G DDR4",
    "微星 PRO A620M-E",
    "微星 B550M PRO-VDH WIFI",
    "金士顿 FURY 野兽 DDR4 3200 8G×2",
    "海盗船 LPX DDR4 3200 8G×2",
    "金士顿 A400 480G",
    "金士顿 NV2 1T",
    "英睿达 P3 Plus 1T",
    "微星 MAG A650BN",
    "海盗船 CX650M",
    "华硕 PRIME AP201",
    "分形工艺 Pop Air",
    "利民 PA120 SE",
    "九州风神 AG400",
]

SOURCE_MAP = {"1": "淘宝/天猫", "2": "京东", "3": "拼多多", "4": "苏宁", "5": "唯品会", "7": "抖音", "8": "快手"}


def run_search(kw):
    proc = subprocess.run(
        [UV, "run", "scripts/main.py", "search", "--source=0", "--keyword=" + kw],
        cwd=SKILL_DIR,
        capture_output=True,
        timeout=90,
    )
    out = proc.stdout.decode("utf-8", "replace")
    err = proc.stderr.decode("utf-8", "replace")
    if proc.returncode != 0:
        return {"error": err.strip()[-300:] or f"exit {proc.returncode}"}
    reader = csv.DictReader(io.StringIO(out))
    rows = []
    for i, r in enumerate(reader):
        if i >= 5:
            break
        rows.append(
            {
                "商品名": r.get("title"),
                "平台": SOURCE_MAP.get(str(r.get("source")), str(r.get("source"))),
                "店铺": r.get("shopName"),
                "原价": float(r["originalPrice"]) if r.get("originalPrice") else None,
                "参考价": float(r["actualPrice"]) if r.get("actualPrice") else None,
                "券额": float(r["couponPrice"]) if r.get("couponPrice") else None,
                "月销": r.get("monthSales"),
                "goodsId": r.get("goodsId"),
            }
        )
    return {"rows": rows}


def main():
    results = {}
    for kw in KEYWORDS:
        print(f"searching: {kw}", flush=True)
        try:
            results[kw] = run_search(kw)
        except Exception as e:
            results[kw] = {"error": str(e)}
        time.sleep(1)
    with open(OUT, "w", encoding="utf-8") as f:
        json.dump(results, f, ensure_ascii=False, indent=2)
    print("SAVED", OUT)


if __name__ == "__main__":
    main()
