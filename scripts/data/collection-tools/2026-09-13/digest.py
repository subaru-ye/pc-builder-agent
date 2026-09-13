# -*- coding: utf-8 -*-
"""把新候选池按类别聚成"身份簇"摘要,供复核挑出新型号。

每行输出: [类别] sig | 行数 | 最低价 | 店铺数 | 样例标题(截断)
sig 提取是粗签名(品牌+芯片/型号+容量),精确身份由复核定。

用法:python digest.py cpu|gpu|motherboard|memory|ssd|psu|case|cooler|all
"""
from __future__ import annotations

import json
import re
import sys
from collections import defaultdict
from pathlib import Path

HERE = Path(__file__).resolve().parent
RAW = HERE.parents[3] / "var/data/collections/2026-09-13-maishou-expansion-01"

BRANDS = {
    "华硕": "asus", "asus": "asus", "技嘉": "gigabyte", "gigabyte": "gigabyte", "微星": "msi", "msi": "msi",
    "蓝宝石": "sapphire", "sapphire": "sapphire", "七彩虹": "colorful", "colorful": "colorful",
    "影驰": "galax", "galax": "galax", "索泰": "zotac", "zotac": "zotac", "撼讯": "powercolor",
    "讯景": "xfx", "xfx": "xfx", "蓝戟": "sparkle", "盈通": "yeston", "铭瑄": "maxsun",
    "耕升": "gainward", "映众": "inno3d", "华擎": "asrock", "asrock": "asrock",
    "金百达": "kingbank", "光威": "gloway", "玖合": "jiuhe", "科赋": "klevv", "宏碁": "acer",
    "掠夺者": "acer", "金士顿": "kingston", "kingston": "kingston", "海盗船": "corsair",
    "corsair": "corsair", "芝奇": "gskill", "威刚": "adata", "英睿达": "crucial", "crucial": "crucial",
    "十铨": "teamgroup", "致态": "zhitai", "三星": "samsung", "西数": "wd", "西部数据": "wd",
    "梵想": "fanxiang", "雷克沙": "lexar", "鑫谷": "segotep", "长城": "greatwall", "航嘉": "huntkey",
    "全汉": "fsp", "振华": "superflower", "安钛克": "antec", "先马": "sama", "九州风神": "deepcool",
    "联力": "lianli", "乔思伯": "jonsbo", "利民": "thermalright", "thermalright": "thermalright",
    "瓦尔基里": "valkyrie", "猫头鹰": "noctua", "超频三": "pccooler", "酷冷至尊": "coolermaster",
    "coolermaster": "coolermaster", "爱国者": "aigo", "分形工艺": "fractal", "fractal": "fractal",
    "nzxt": "nzxt", "英特尔": "intel", "intel": "intel", "amd": "amd", "锐龙": "amd",
}

def sq(s: str) -> str:
    s = str(s or "").lower()
    s = re.sub(r"g\s*[x×*]\s*(\d)", r"g\1", s)
    return re.sub(r"[^a-z0-9\u4e00-\u9fff]", "", s)

def brand_of(s: str) -> str:
    for k, v in BRANDS.items():
        if k in s:
            return v
    return "?"

def sig_cpu(s: str) -> str:
    m = re.search(r"(i3|i5|i7|i9)(\d{4,5}[fk]?)", s)
    if m:
        return f"intel {m.group(1)}-{m.group(2)}"
    m = re.search(r"ultra\s?(5|7|9)(\d{3}[kf]?)", s)
    if m:
        return f"intel ultra{m.group(1)} {m.group(2)}"
    m = re.search(r"(?:锐龙|ryzen)?\s?r?(3|5|7|9)(\d{4})(x3d|x|g3d|g|gt|f|xt)?", s)
    if m:
        return f"amd r{m.group(1)} {m.group(2)}{m.group(3) or ''}"
    return "?"

def sig_gpu(s: str) -> str:
    m = re.search(r"(rtx|gtx|rx|arc)\s?(3050|4050|4060|4070|5050|5060|5070|5080|5090|6600|6750|7600|7700|7800|7900|9060|9070|9080|b570|b580|a750|a770)(ti|super|xt|xtx)?\s?(\d+g)?", s)
    if not m:
        return "?"
    chip = f"{m.group(1)} {m.group(2)}{m.group(3) or ''}"
    mem = m.group(4) or ""
    return f"{chip}{mem}"

def sig_mb(s: str) -> str:
    m = re.search(r"(x870e?|b850m?e?|b860m?|b650m?e?|b760m?|b660m?|b550m?|b450m?|x670e?|z790|z890|h610m?|h810m?|a520m?|a620m?)(i)?", s)
    chip = m.group(1) if m else "?"
    itx = (m.group(2) or "") if m else ""
    ddr = "d4" if re.search(r"ddr4|d4", s) else ("d5" if re.search(r"ddr5|d5", s) else "")
    return f"{chip}{itx}{ddr}"

def sig_mem(s: str) -> str:
    gen = "d5" if re.search(r"ddr5|d5", s) else ("d4" if re.search(r"ddr4|d4", s) else "?")
    m = re.search(r"(\d{2,4}g)\s?(\d{1,2})?g?×?(\d)?", s)  # 第一容量 token
    m2 = re.search(r"(\d{4})\s?(?:mts|频率)?", s)
    speed = ""
    for sp in re.findall(r"(?:d[dr]\d?)?(\d{4})(?!\d)", s):
        if sp in {"3200", "3600", "5200", "5600", "6000", "6400", "6800", "6001", "7200", "8000"}:
            speed = sp
            break
    return f"{gen}{m.group(1) if m else ''}{speed}"

def sig_ssd(s: str) -> str:
    m = re.search(r"(sn\d{3,4}x?|nv\d|p\d{1,3}(?:plus)?|t\d{3}|tiplus\d{4}|ti\d{3}|s\d{3}pro|nm\d{3,4}|970evo|980pro|990evo|990pro|870evo|870qvo|bx\d{3}|mx\d{3}|kc\d{4}|a400|c\d{4})", s)
    cap = ""
    for c in re.findall(r"(500g|512g|1tb?|2tb?|4tb?)", s):
        cap = c
        break
    return f"{m.group(1) if m else '?'}{cap}"

def sig_psu(s: str) -> str:
    m = re.search(r"(\d{3})w", s)
    return f"{m.group(1)}w" if m else "?"

def sig_case(s: str) -> str:
    return ""

def sig_cooler(s: str) -> str:
    m = re.search(r"(\d{3})\s?(?:水冷|风冷)?", s)
    t = "aio" if re.search(r"水冷|一体式|liquid|240|360", s) else "air"
    return f"{t}{m.group(1) or ''}" if m else t

SIGS = {"cpu": sig_cpu, "gpu": sig_gpu, "motherboard": sig_mb, "memory": sig_mem,
        "ssd": sig_ssd, "psu": sig_psu, "case": sig_case, "cooler": sig_cooler}


def main() -> int:
    want = sys.argv[1] if len(sys.argv) > 1 else "all"
    rows = [json.loads(l) for l in (RAW / "pool_for_review.jsonl").read_text(encoding="utf-8").splitlines() if l.strip()]
    clusters: dict[str, dict[str, list]] = defaultdict(lambda: defaultdict(list))
    for r in rows:
        if "junk_words" in r["flags"]:
            continue
        cat = r["category"]
        if want != "all" and cat != want:
            continue
        s = sq(r["title"])
        sig = f"{brand_of(s)} {SIGS[cat](s)}".strip()
        clusters[cat][sig].append(r)
    for cat in sorted(clusters):
        print(f"===== {cat}")
        items = sorted(clusters[cat].items(), key=lambda kv: -len(kv[1]))
        for sig, rs in items:
            if sig.strip("? ") == "?" or not sig.replace("?", "").strip():
                continue
            prices = [r["price_cny"] for r in rs if r["price_cny"]]
            shops = len({r["shop"] for r in rs})
            sample = rs[0]["title"][:52]
            print(f"  {sig:<28} n={len(rs):<3} min={min(prices) if prices else '-':<8} shops={shops:<3} | {sample}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
