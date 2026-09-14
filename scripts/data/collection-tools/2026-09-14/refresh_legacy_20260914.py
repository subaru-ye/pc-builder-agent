# -*- coding: utf-8 -*-
"""2026-09-14 旧 SKU 价格刷新:重扫 07-28 沿用的 57 个 SKU(54 个有新数据行)。

在 09-13 工具(v3.2)基础上:
  - sq() 增加"4位数字+gb"粘合修复(DDR5 6000 32GB → 6000w32gb),内存频率检测此前对
    "频率 容量"相邻标题完全失效(这也是 09-13 内存 0 接受的根因)
  - TAIL_ID 补全 case/cooler/mem/psu/gpu/mb 缺失身份 token(此前空 core 会弱绑定)
  - 内存非 MEM_SPEC SKU 的频率并入 TAIL_ID(频率既是身份也是区分子型号的依据)
判例与守卫同 09-13:v3.1 尾部绑定/非京东全标题单型号/系列精修/散片后缀判定/
跨品牌与改版 FORB/logical 去重/盒装优先/合理带 [0.55, 1.5]。
用法:python refresh_legacy_20260914.py [--emit]  (默认 dry-run 打印逐 SKU 提案)
产出:scripts/data/prices/2026-09-14.csv + catalog-candidates/2026-09-14/legacy-refresh.jsonl
"""
from __future__ import annotations

import argparse
import csv
import json
import re
import sys
from collections import defaultdict
from pathlib import Path

from legacy_patterns import LEGACY_PATTERNS

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[3]
HITS = ROOT / "scripts/data/catalog-candidates/2026-09-14/legacy-hits.jsonl"
CANDS = ROOT / "scripts/data/catalog-candidates/2026-09-13/candidates.jsonl"
PREV_CSV = ROOT / "scripts/data/prices/2026-09-13.csv"
OUT_CSV = ROOT / "scripts/data/prices/2026-09-14.csv"
OUT_DECISIONS = ROOT / "scripts/data/catalog-candidates/2026-09-14/legacy-refresh.jsonl"

CAPTURED_AT = "2026-09-14"
SOURCE = "maishou88"
ZONE = 20          # 尾部绑定窗口(sq 字符)
ZONE_MEM = 26      # 内存标题紧凑,窗口放宽
SANITY_LO, SANITY_HI = 0.55, 1.5

JUNK = ["拆机", "二手", "准新", "坏", "维修", "回收", "出租", "样品", "询价", "议价", "成新",
        "矿卡", "整机", "板u", "主板cpu套装", "冷头", "模组线", "线材", "支架", "延长线",
        "展机", "挡板", "理线", "集线器", "电源线", "数据线", "防尘罩", "保护套", "扩展坞",
        "电脑主机", "电竞主机", "游戏主机"]


def sq(s: str) -> str:
    s = str(s or "").lower()
    s = re.sub(r"g\s*[x×*]\s*(\d)", r"g\1", s)
    # "DDR5 6000 32GB"剥空格后粘成 ddr5600032gb,频率 6000 无法用(\d{4})(?!\d)识别:
    # 在 4 位数字后面紧跟"容量 gb/g2"的边界插入 w 标记(6000w32gb),使其保持可切分
    s = re.sub(r"(\d{4})(?=\s*\d{1,3}\s*g(?:b)?(?:[24])?)", r"\1w", s)
    return re.sub(r"[^a-z0-9\u4e00-\u9fff]", "", s)


# ---------------------------------------------------------------- 守卫表
B_GB = r"技嘉|gigabyte|魔鹰|猎鹰|小雕|雪鹰|aorus|windforce"
B_MSI = r"微星|msi|ventus|万图师|魔龙|gamingx|suprim|超龙|edge"
B_ASUS = r"华硕|asus|tuf|猛禽|strix|雪豹|dual"
B_SAPPHIRE = r"蓝宝石|sapphire|pulse|超白金|nitro|pure"

MEM_MULTI = r"(\d{1,2})gb?(\d{1,2})gb?(?!\s*x?\s*[24])"  # "16g32g"式混列(区别于 32GB(16GBx2))

REQS: dict[str, list[str]] = defaultdict(list)
FORBS: dict[str, list[str]] = defaultdict(list)
for _sku in LEGACY_PATTERNS:
    _cat = _sku.split("-")[0]
    if _cat == "cpu":
        FORBS[_sku] += [r"搭", r"套装", r"主板"]
    if _cat == "mem":
        FORBS[_sku] += [MEM_MULTI]

BRAND: dict[str, str] = {
    "gpu-gb-5060ti-windforce-16g": B_GB, "gpu-gb-5060-windforce": B_GB,
    "gpu-gb-4070-windforce": B_GB, "gpu-gb-5070-windforce-sff": B_GB,
    "gpu-gb-5080-gaming": B_GB,
    "gpu-msi-3060-ventus2x": B_MSI, "gpu-msi-5070ti-ventus3x": B_MSI,
    "gpu-msi-4060-ventus2x-black": B_MSI,
    "gpu-asus-4060ti-dual-evo": B_ASUS, "gpu-asus-4070tis-tuf": B_ASUS,
    "gpu-asus-4080s-tuf": B_ASUS,
    "gpu-sapphire-9070xt-pulse": B_SAPPHIRE, "gpu-sapphire-7900xt-pulse": B_SAPPHIRE,
    "gpu-sapphire-7600-pulse": B_SAPPHIRE, "gpu-sapphire-9060xt-pulse-16g": B_SAPPHIRE,
    "gpu-sapphire-7700xt-pulse": B_SAPPHIRE, "gpu-sapphire-7800xt-pulse": B_SAPPHIRE,
    "gpu-intel-b580-le": r"英特尔|intel|b580",
    "mb-msi-z790-p-wifi": B_MSI, "mb-msi-b760m-a-wifi": B_MSI,
    "mb-msi-b650m-mortar-wifi": B_MSI, "mb-msi-b860m-a-wifi": B_MSI,
    "mb-gb-x870-eagle-wifi7": B_GB, "mb-gb-b650i-aorus-ultra": B_GB,
    "mem-crucial-ballistix-16-3200": r"英睿达|crucial|ballistix|铂胜",
    "mem-kingston-beast-16-6000": r"金士顿|kingston",
    "mem-kingston-beast-32-5600": r"金士顿|kingston",
    "mem-gskill-s5-32-6000": r"芝奇|gskill|ripjaws",
    "mem-gskill-flarex5-32-6000": r"芝奇|gskill",
    "mem-corsair-veng-32-6000": r"海盗船|corsair|vengeance|复仇者",
    "ssd-crucial-t500-2tb": r"英睿达|crucial",
    "ssd-crucial-mx500-1tb": r"英睿达|crucial|美光",
    "ssd-kingston-nv2-1tb": r"金士顿|kingston",
    "ssd-kingston-kc3000-1tb": r"金士顿|kingston",
    "ssd-kingston-a400-480gb": r"金士顿|kingston",
    "ssd-crucial-p3plus-1tb": r"英睿达|crucial",
    "ssd-crucial-bx500-1tb": r"英睿达|crucial",
    "ssd-wd-sn770-1tb": r"西数|西部数据|wd|wdc",
    "ssd-wd-sn580-1tb": r"西数|西部数据|wd|wdc",
    "ssd-wd-sn850x-2tb": r"西数|西部数据|wd|wdc",
    "ssd-samsung-990pro-2tb": r"三星|samsung",
    "psu-msi-mag-a650bn": B_MSI,
    "psu-corsair-rm1000x-2021": r"海盗船|corsair",
    "psu-corsair-rm850x-2021": r"海盗船|corsair",
    "psu-corsair-rm750e": r"海盗船|corsair",
    "case-coolermaster-nr200p": r"酷冷|coolermaster",
    "case-asus-prime-ap201": r"华硕|asus|prime",
    "cooler-deepcool-ag400": r"deepcool|九州风神|玄冰",
    "cooler-thermalright-pa120se": r"利民|thermalright",
    "cooler-deepcool-ak620": r"deepcool|九州风神|玄冰",
}
for _sku, _b in BRAND.items():
    REQS[_sku] += [_b]

EXTRA_REQ = {
    "gpu-gb-5060ti-windforce-16g": [r"16g"],
    "gpu-gb-5060-windforce": [r"风魔|windforce"],
    "gpu-gb-4070-windforce": [r"风魔|windforce"],
    "gpu-gb-5070-windforce-sff": [r"sff"],
    "gpu-asus-4060ti-dual-evo": [r"8g"],
    "gpu-msi-3060-ventus2x": [r"12g"],
    "gpu-sapphire-9060xt-pulse-16g": [r"16g"],
    "mb-msi-b760m-a-wifi": [r"b760mawifi"],
    "mb-msi-b860m-a-wifi": [r"b860mawifi"],
    "psu-msi-mag-a650bn": [r"a650bn"],
    "psu-corsair-rm1000x-2021": [r"rm1000x"],
    "psu-corsair-rm850x-2021": [r"rm850x"],
    "psu-corsair-rm750e": [r"rm750e"],
}
# 异品牌名(同行多品牌混列无法绑定);主板多一个华擎
X_GB = r"七彩虹|colorful|华硕|asus|微星|msi|影驰|galax|索泰|zotac|铭瑄|盈通|yeston|昂达|onda|万丽|耕升|gainward"
X_MSI = r"七彩虹|colorful|技嘉|gigabyte|华硕|asus|影驰|galax|索泰|zotac|铭瑄|盈通|yeston|昂达|万丽|耕升|gainward"
X_ASUS = r"七彩虹|colorful|技嘉|gigabyte|微星|msi|影驰|galax|索泰|zotac|铭瑄|盈通|yeston|昂达|耕升|gainward"
X_SAPPHIRE = r"华硕|asus|技嘉|gigabyte|微星|msi|七彩虹|colorful|盈通|yeston|撼讯|powercolor|迪兰|讯景|xfx|瀚铠|希仕"
X_MB_MSI = r"技嘉|gigabyte|华硕|asus|华擎|asrock|七彩虹|colorful"
X_MB_GB = r"微星|msi|华硕|asus|华擎|asrock|七彩虹|colorful"

EXTRA_FORB = {
    "gpu-gb-5060ti-windforce-16g": [r"8g.{0,14}16g|16g.{0,14}8g", X_GB],
    "gpu-gb-5060-windforce": [X_GB],
    "gpu-gb-4070-windforce": [X_GB],
    "gpu-gb-5070-windforce-sff": [X_GB],
    "gpu-gb-5080-gaming": [X_GB],
    "gpu-asus-4060ti-dual-evo": [r"16g", X_ASUS],
    "gpu-asus-4070tis-tuf": [X_ASUS],
    "gpu-asus-4080s-tuf": [X_ASUS],
    "gpu-msi-3060-ventus2x": [r"12g.{0,14}8g|8g.{0,14}12g", r"3060ti", X_MSI],
    "gpu-msi-4060-ventus2x-black": [X_MSI],
    "gpu-msi-5070ti-ventus3x": [X_MSI],
    "gpu-intel-b580-le": [r"b570", r"蓝戟|sparkle|华擎|asrock|gunnir|磐镭|photon|challenger|steellegend"],
    "gpu-sapphire-7600-pulse": [X_SAPPHIRE],
    "gpu-sapphire-7700xt-pulse": [X_SAPPHIRE],
    "gpu-sapphire-7800xt-pulse": [X_SAPPHIRE],
    "gpu-sapphire-7900xt-pulse": [r"7900xtx", X_SAPPHIRE],
    "gpu-sapphire-9060xt-pulse-16g": [r"8g.{0,14}16g|16g.{0,14}8g", X_SAPPHIRE],
    "gpu-sapphire-9070xt-pulse": [X_SAPPHIRE],
    "mb-msi-z790-p-wifi": [X_MB_MSI],
    "mb-msi-b760m-a-wifi": [r"ddr4", X_MB_MSI],
    "mb-msi-b650m-mortar-wifi": [X_MB_MSI],
    "mb-msi-b860m-a-wifi": [X_MB_MSI],
    "mb-gb-x870-eagle-wifi7": [X_MB_GB],
    "mb-gb-b650i-aorus-ultra": [X_MB_GB],
    "mb-msi-h610m-g-ddr4": [r"ddr5"],
    "psu-msi-mag-a650bn": [r"海盗船|corsair|航嘉|huntkey|长城"],
    "cooler-deepcool-ag400": [r"ag400pro|ag400digital|幻彩|ag400g2|二代|玄冰400(?!ag)"],
    "cooler-deepcool-ak620": [r"ak620digital|数显"],
    "cooler-noctua-nh-d15": [r"chromax"],
    "cooler-noctua-nh-u12s": [r"redux"],
    "cooler-thermalright-pa120se": [r"ax120"],
    "ssd-wd-sn770-1tb": [r"sn770m"],
    "case-fractal-define-7": [r"define7xl", r"define7c"],
    "case-fractal-meshify-2-compact": [r"meshify2xl"],
    "case-fractal-pop-air": [r"popmini"],
    "case-corsair-4000d-airflow": [r"rgbaf"],
    "case-corsair-5000d-airflow": [r"core"],
    "case-nzxt-h5-flow-2024": [r"rgb"],
    "mb-msi-b650-tomahawk-wifi": [r"gamingplus|刀锋"],
    "psu-corsair-rm750e": [r"机箱|水冷"],
    "cooler-arctic-lf2-240": [r"freezeriii|lf3"],
}
for _sku, _rs in EXTRA_REQ.items():
    REQS[_sku] += _rs
for _sku, _rs in EXTRA_FORB.items():
    FORBS[_sku] += _rs

# 电源瓦数:其他瓦数(带 w 形态)排除;身份 token 带自家瓦数
_W_OF = {"psu-msi-mag-a650bn": "650w", "psu-corsair-rm1000x-2021": "1000w",
         "psu-corsair-rm850x-2021": "850w", "psu-corsair-rm750e": "750w"}
for _sku, _w in _W_OF.items():
    FORBS[_sku] += [rf"{w}" for w in ("650w", "750w", "850w", "1000w") if w != _w]

# 内存:频率 + 容量(系列 token)
MEM_SPEC = {
    "mem-crucial-ballistix-16-3200": (16, 3200, r"ballistix|铂胜"),
    "mem-kingston-beast-16-6000": (16, 6000, r"beast|野兽"),
    "mem-kingston-beast-32-5600": (32, 5600, r"beast|野兽"),
    "mem-gskill-s5-32-6000": (32, 6000, r"ripjawss5|幻锋戟"),
    "mem-gskill-flarex5-32-6000": (32, 6000, r"flarex5|焰锋戟"),
    "mem-corsair-veng-32-6000": (32, 6000, r"vengeance|veng|复仇者"),
}

SSD_CAP = {
    "ssd-crucial-t500-2tb": 2, "ssd-crucial-mx500-1tb": 1, "ssd-kingston-nv2-1tb": 1,
    "ssd-kingston-kc3000-1tb": 1, "ssd-crucial-p3plus-1tb": 1, "ssd-crucial-bx500-1tb": 1,
    "ssd-wd-sn770-1tb": 1, "ssd-wd-sn580-1tb": 1, "ssd-wd-sn850x-2tb": 2,
    "ssd-samsung-990pro-2tb": 2, "ssd-kingston-a400-480gb": 480,
}

# 非 MEM_SPEC 内存 SKU 的容量守卫(不进 TAIL_ID,避免 32g/16g 污染同品类 other 集合)
MEM_CAP: dict[str, int] = {
    "mem-adata-lancer-32-5200": 32, "mem-corsair-veng-rgb-32-6000": 32,
    "mem-gskill-ripjawsv-32-3200": 32, "mem-gskill-ripjawsv-16-3600": 16,
    "mem-gskill-z5-rgb-32-6400": 32, "mem-gskill-z5neo-rgb-32-6000": 32,
    "mem-team-delta-32-3200-d4": 32, "mem-team-delta-32-6000-white": 32,
    "mem-kingston-beast-16-3200-d4": 16, "mem-kingston-beast-16-5200": 16,
    "mem-kingston-beast-32-3600-d4": 32, "mem-kingston-beast-32-6000": 32,
    "mem-corsair-lpx-16-3200": 16, "mem-corsair-lpx-32-3600": 32,
}
for _sku, _cap in MEM_CAP.items():
    FORBS[_sku] += [r"2x8"] if _cap == 32 else [r"2x16"]

# b580-le 是 Intel 官方 Limited Edition;任何板卡合作伙伴行(iCraft/蓝戟/Sparkle 等)不得冒充
FORBS["gpu-intel-b580-le"] += [
    r"icraft|milestone|铭瑄|蓝戟|gunnir|sparkle|撼与|华擎|asrock|宏碁|acer",
    r"七彩虹|colorful|索泰|zotac|影驰|galax|耕升|gainward|盈通|yeston|昂达|万丽|manli",
]
# 4000D 目录身份为 Airflow;无"风流/airflow"字样的行可能是普通版 4000D,不冒充
REQS["case-corsair-4000d-airflow"] += [r"风流|airflow"]
# VENTUS 2X 目录为非 OC;选中变体尾部带 OC 的行是另一型号
FORBS["gpu-msi-3060-ventus2x"] += [r"oc"]

# 系列精修:sku 名内嵌系列(-pulse)的,选中变体后缀必须含该系列 token
# (家族前缀会罗列 极地/脉动/氮动,故查身份 token 之后的后缀,而非全标题)
SERIES_SUFFIX = {
    "gpu-sapphire-7600-pulse": r"脉动|pulse",
    "gpu-sapphire-6600-pulse": r"脉动|pulse",
    "gpu-sapphire-7700xt-pulse": r"脉动|pulse",
    "gpu-sapphire-7800xt-pulse": r"脉动|pulse",
    "gpu-sapphire-7900xt-pulse": r"脉动|pulse",
    "gpu-sapphire-9060xt-pulse-16g": r"脉动|pulse",
    "gpu-sapphire-9070xt-pulse": r"脉动|pulse",
}

TRAY = re.compile(r"散片")

# ---------------------------------------------------------------- 身份 token
TAIL_ID: dict[str, list[str]] = {
    "cpu-i3-12100f": ["12100f"], "cpu-i5-12400f": ["12400f"], "cpu-i5-12600k": ["12600k", "12600kf"],
    "cpu-i5-13400f": ["13400f"], "cpu-i5-14600kf": ["14600k", "14600kf"], "cpu-i7-14700k": ["14700k", "14700kf"],
    "cpu-r5-5500": ["5500"], "cpu-r5-5600": ["5600"], "cpu-r5-7500f": ["7500f"],
    "cpu-r5-7600": ["7600"], "cpu-r5-7600x": ["7600x"], "cpu-r5-9600x": ["9600x"],
    "cpu-r7-5700x": ["5700x"], "cpu-r7-7700": ["7700"], "cpu-r7-7800x3d": ["7800x3d"],
    "cpu-r7-9700x": ["9700x"], "cpu-r7-9800x3d": ["9800x3d"], "cpu-r9-9900x": ["9900x"],
    "cpu-ultra5-245k": ["245k"],
    "gpu-asus-4060ti-dual-evo": ["4060ti"], "gpu-asus-4070tis-tuf": ["4070tis", "4070tisuper"],
    "gpu-asus-4080s-tuf": ["4080s", "4080super"], "gpu-gb-4070-windforce": ["4070", "风魔"],
    "gpu-gb-5060-windforce": ["5060", "风魔"], "gpu-gb-5060ti-windforce-16g": ["5060ti", "风魔"],
    "gpu-gb-5070-windforce-sff": ["5070", "sff"], "gpu-gb-5080-gaming": ["5080", "魔鹰"],
    "gpu-intel-b580-le": ["b580"], "gpu-msi-3060-ventus2x": ["3060"],
    "gpu-msi-4060-ventus2x-black": ["4060"], "gpu-msi-5070ti-ventus3x": ["5070ti"],
    "gpu-sapphire-7600-pulse": ["7600"], "gpu-sapphire-6600-pulse": ["6600"],
    "gpu-sapphire-7700xt-pulse": ["7700xt"],
    "gpu-sapphire-7800xt-pulse": ["7800xt"], "gpu-sapphire-7900xt-pulse": ["7900xt"],
    "gpu-sapphire-9060xt-pulse-16g": ["9060xt"], "gpu-sapphire-9070xt-pulse": ["9070xt"],
    "gpu-msi-4070s-ventus2x": ["4070s", "4070super"],
    "mb-gb-b550-aorus-elite-v2": ["b550aoruselitev2"],
    "mb-gb-b650i-aorus-ultra": ["b650iaorus"], "mb-gb-x870-eagle-wifi7": ["x870eagle"],
    "mb-msi-b650m-mortar-wifi": ["b650mmortar"], "mb-msi-b760m-a-wifi": ["b760mawifi"],
    "mb-msi-b860m-a-wifi": ["b860mawifi"], "mb-msi-z790-p-wifi": ["z790p"],
    "mem-adata-lancer-32-5200": ["lancer", "5200"],
    "mem-corsair-veng-rgb-32-6000": ["复仇者rgb", "6000"],
    "mem-gskill-ripjawsv-32-3200": ["ripjawsv", "3200"],
    "mem-gskill-ripjawsv-16-3600": ["ripjawsv", "3600"],
    "mem-gskill-z5-rgb-32-6400": ["tridentz5rgb", "6400"],
    "mem-gskill-z5neo-rgb-32-6000": ["tridentz5neo", "6000"],
    "mem-team-delta-32-3200-d4": ["delta", "3200"],
    "mem-kingston-beast-16-3200-d4": ["beast", "3200"],
    "mem-kingston-beast-16-5200": ["beast", "5200"],
    "mem-kingston-beast-32-3600-d4": ["beast", "3600"],
    "mem-corsair-lpx-16-3200": ["lpx", "3200"],
    "mem-corsair-lpx-32-3600": ["lpx", "3600"],
    "ssd-crucial-bx500-1tb": ["bx500"], "ssd-crucial-p3plus-1tb": ["p3plus"],
    "ssd-crucial-mx500-1tb": ["mx500"],
    "ssd-crucial-t500-2tb": ["t500"], "ssd-kingston-a400-480gb": ["a400"],
    "ssd-kingston-kc3000-1tb": ["kc3000"], "ssd-kingston-nv2-1tb": ["nv2"],
    "ssd-samsung-990pro-2tb": ["990pro"], "ssd-wd-sn580-1tb": ["sn580"],
    "ssd-wd-sn770-1tb": ["sn770"], "ssd-wd-sn850x-2tb": ["sn850x"],
    "psu-corsair-rm1000x-2021": ["rm1000x", "1000w"], "psu-corsair-rm750e": ["rm750e", "750w"],
    "psu-corsair-rm850x-2021": ["rm850x", "850w"], "psu-corsair-rm850e": ["rm850e", "850w"],
    "psu-msi-mag-a650bn": ["a650bn", "650w"],
    "psu-corsair-cx650m": ["cx650m", "650w"], "psu-corsair-hx1000i-2022": ["hx1000i", "1000w"],
    "psu-gb-ud850gm-pg5": ["ud850gm", "850w"],
    "psu-msi-mag-a750gl": ["a750gl", "750w"], "psu-msi-mag-a850gl": ["a850gl", "850w"],
    "cpu-r9-7900": ["7900"],
    "mb-asrock-b650m-hdv-m2": ["b650mhdv"], "mb-asus-b650e-f-strix": ["b650ef"],
    "mb-gb-b650-aorus-elite-ax": ["b650aoruselite"], "mb-gb-b760-gaming-x-ax": ["b760gamingx"],
    "mb-msi-b550-tomahawk": ["b550tomahawk"], "mb-msi-b550m-pro-vdh-wifi": ["b550mprovdh"],
    "mb-msi-b650-tomahawk-wifi": ["b650tomahawk"],
    "mb-msi-b760m-a-wifi-ddr4": ["b760mawifi", "ddr4"],
    "mb-msi-b850-tomahawk-max": ["b850tomahawk"], "mb-msi-b860-tomahawk-wifi": ["b860tomahawk"],
    "mb-msi-h610m-g-ddr4": ["h610mg", "ddr4"], "mb-msi-pro-a620m-e": ["a620me"],
    "mb-msi-z890-tomahawk-wifi": ["z890tomahawk"],
    "mem-kingston-beast-32-6000": ["beast", "6000"],
    "mem-team-delta-32-6000-white": ["delta", "6000"],
    "ssd-adata-legend800-1tb": ["legend800"], "ssd-adata-s70blade-1tb": ["s70blade"],
    "ssd-crucial-p5plus-1tb": ["p5plus"], "ssd-intel-670p-1tb": ["670p"],
    "ssd-samsung-870evo-1tb": ["870evo"], "ssd-samsung-870qvo-2tb": ["870qvo"],
    "ssd-samsung-970evoplus-1tb": ["970evoplus"], "ssd-samsung-980pro-1tb": ["980pro"],
    "ssd-wd-sa510-1tb": ["sa510"],
    "case-bequiet-shadow-base-800-fx": ["shadowbase800fx"],
    "case-coolermaster-td500-mesh-v2": ["td500mesh"],
    "case-fractal-north": ["north"], "case-fractal-pop-air": ["popair"],
    "case-fractal-terra": ["terra"], "case-fractal-torrent": ["torrent"],
    "case-lianli-a3-matx": ["a3matx"], "case-lianli-lancool-216": ["lancool216"],
    "case-lianli-o11-dynamic-evo": ["o11"],
    "cooler-arctic-lf3-240": ["liquidfreezeriii", "240"],
    "cooler-arctic-lf3-360": ["liquidfreezeriii", "360"],
    "cooler-bequiet-dark-rock-pro-5": ["darkrockpro5"],
    "cooler-coolermaster-hyper212-black": ["hyper212"],
    "cooler-deepcool-assassin-iv": ["assassiniv"], "cooler-deepcool-lt520": ["lt520"],
    "cooler-noctua-nh-d15": ["nhd15"], "cooler-noctua-nh-u12s": ["nhu12s"],
    "cooler-thermalright-frozen-prism-240": ["frozenprism", "240"],
    "cooler-thermalright-ps120se": ["ps120se"],
    "psu-msi-mpg-a850g": ["a850g", "850w"],
    "psu-seasonic-focus-gx750-atx30": ["gx750", "750w"],
    "psu-seasonic-focus-gx850-atx30": ["gx850", "850w"],
    "psu-seasonic-vertex-gx1000": ["vertexgx1000", "1000w"],
    "psu-bq-pp12m-750": ["purepower12", "750w"], "psu-bq-pp12m-850": ["purepower12", "850w"],
    "psu-bq-sp12-750": ["straightpower12", "750w"], "psu-bq-sp12-1000": ["straightpower12", "1000w"],
    "psu-tt-gf3-750": ["gf3", "750w"], "psu-tt-gf3-850": ["gf3", "850w"],
    "case-asus-prime-ap201": ["ap201"], "case-coolermaster-nr200p": ["nr200p"],
    "case-bequiet-pure-base-500dx": ["purebase500dx"],
    "case-corsair-4000d-airflow": ["4000d"], "case-corsair-5000d-airflow": ["5000d"],
    "case-fractal-define-7": ["define7"], "case-fractal-meshify-2-compact": ["meshify2compact"],
    "case-montech-air-903-max": ["air903max"],
    "case-nzxt-h5-flow-2024": ["h5flow"], "case-nzxt-h6-flow-2023": ["h6flow"],
    "case-nzxt-h7-flow-2024": ["h7flow"],
    "cooler-arctic-lf2-240": ["liquidfreezerii", "240"],
    "cooler-bequiet-dark-rock-pro-4": ["darkrockpro4"],
    "cooler-corsair-h100i-elite-capellix-xt": ["h100iecxt", "240"],
    "cooler-corsair-h150i-elite-capellix-xt": ["h150iecxt", "360"],
    "cooler-msi-mag-coreliquid-e360": ["coreliquide360"],
    "cooler-nzxt-kraken-240": ["kraken240"], "cooler-nzxt-kraken-x63": ["krakenx63"],
    "cooler-deepcool-ag400": ["ag400"], "cooler-deepcool-ak620": ["ak620"],
    "cooler-thermalright-pa120se": ["pa120se"],
}

EXTRA_LITS = {
    "cpu": ["10100f", "13100f", "14100f", "12490f", "13490f", "14400f", "14500", "13900k",
            "14900k", "12900k", "8700f", "7400f", "5700x3d", "5500gt", "5600gt", "5600g",
            "5700g", "5600x", "7950x", "7900x", "9950x", "9950x3d", "9900x3d", "9850x3d",
            "285k", "265k", "255k", "7600x3d", "5950x", "7900", "8600g", "8500g", "8600f",
            "8400f", "9500f", "3600", "3800x", "3400g", "3000g", "3200", "4600", "4500",
            "1700", "2700x", "3600x", "12500", "13700t", "12700t", "13700kf", "12700kf",
            "7700x", "7700f"],
    "gpu": ["5090", "5080", "5050", "4090d", "4090", "4080", "4070ti", "3060ti", "9070gre",
            "9070", "7900xtx", "7900gre", "7600xt", "6600", "6600xt", "6650xt", "6700xt",
            "6750xt", "6800", "6800xt", "6900xt", "b570", "a770", "a750", "a580", "a380"],
    "mb": ["x870e", "x670e", "x670", "b650mtomahawk", "b550mtomahawk", "b760mgamingx",
           "b850tomahawk", "b860tomahawk", "b550tomahawk", "b650tomahawk", "b650ef",
           "b650mhdv", "b550aoruselite", "b650aoruseliteax", "b550mprovdh", "h610mg",
           "a620me", "z890tomahawk"],
    "mem": ["lancer", "lpx", "ripjawsv", "ripjaws", "tridentz5rgb", "tridentz5neo",
            "tridentz5", "幻光戟", "delta", "3200", "3600", "5200", "5600", "6000",
            "6400", "6800"],
    "ssd": ["sn5100", "sn7100", "sn8100", "nv3", "980", "990", "970evo", "p3", "p5plus", "mx500",
            "670p", "870evo", "870qvo", "sa510", "legend800", "s70blade", "kc3000aero",
            "sn3000", "sn5000"],
    "psu": ["pp12m", "purepower12", "sp12", "straightpower12", "cx650m", "hx1000i",
            "ud850gm", "a750gl", "a850gl", "a850g", "mpga850g", "gx750", "gx850", "gx1000",
            "vertex", "gf3", "650w", "750w", "850w", "1000w", "a550bn", "a750bn", "a600dn"],
    "case": ["purebase500dx", "shadowbase800fx", "nr200", "td500mesh", "td500", "4000d",
             "5000d", "define7", "define7xl", "meshify2", "meshify2xl", "north", "terra",
             "torrent", "popair", "popmini", "a3matx", "lancool216", "鬼斧216", "l216",
             "o11", "包豪斯", "air903", "h5flow", "h6flow", "h7flow", "evo"],
    "cooler": ["lf2", "lf3", "darkrockpro4", "darkrockpro5", "hyper212", "h100i", "h150i",
               "ag400pro", "ag400digital", "ak620digital", "assassiniv", "lt520",
               "corelique360", "nhd15", "nhu12s", "kraken240", "krakenx63", "frozenprism",
               "ps120se", "chromax", "redux", "240", "360", "ax120", "pa120", "ak400",
               "ak400se", "ak500", "ak500s"],
}

_MEM_SPEED_LITS = {"3200", "3600", "5200", "5600", "6000", "6400", "6800"}


def candidate_tokens() -> dict[str, set[str]]:
    out: dict[str, set[str]] = defaultdict(set)
    if not CANDS.exists():
        return out
    drop = {"gb", "wd", "msi", "asus", "pro", "plus", "max", "se", "xt", "oc", "le", "rgb",
            "white", "black", "sff", "evo", "dual", "tuf", "pulse", "ventus", "windforce",
            "gaming", "ultra", "slim", "ssd", "gskill", "sapphire", "kingston", "crucial",
            "corsair", "deepcool", "thermalright", "noctua", "nzxt", "arctic", "fractal",
            "lianli", "montech", "coolermaster", "bequiet", "seasonic", "asrock", "adata",
            "samsung", "intel", "amd", "valkyrie", "jonsbo", "sama", "flux", "lf", "dk"}
    for line in CANDS.read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        c = json.loads(line)
        cid = c.get("id") or c.get("sku") or ""
        for seg in cid.split("-")[1:]:
            if seg in drop or not (re.search(r"\d", seg) and re.search(r"[a-z]", seg)):
                continue
            if re.fullmatch(r"\d+g|\d+tb|\d{4,}", seg):
                continue
            out[cid.split("-")[0]].add(seg)
    return out


# ---------------------------------------------------------------- 容量/频率解析
def mem_caps(t: str) -> set[int]:
    caps = set()
    for m in re.finditer(r"(\d{1,3})gb?x?([24])?", t):
        n = int(m.group(1))
        if m.group(2):
            n *= int(m.group(2))
        if 4 <= n <= 256:
            caps.add(n)
    return caps


def mem_speeds(t: str) -> set[int]:
    return {int(m) for m in re.findall(r"(\d{4})(?!\d)", t) if int(m) >= 2400}


def ssd_caps(t: str) -> set[int]:
    caps = set()
    for m in re.findall(r"(\d)tb|(\d)t(?![a-z0-9])", t):
        caps.add(int(m[0] or m[1]))
    for m in re.findall(r"(\d{3})gb|(\d{3})g(?![a-z0-9])", t):
        caps.add(int(m[0] or m[1]))
    return {c for c in caps if c > 0}


def tier_a_core(sku: str) -> list[str]:
    """tier-A 绑定核心:身份 token(+容量等判别词)必须全部出现在 zone。"""
    toks = list(TAIL_ID.get(sku, []))
    if sku in MEM_SPEC:
        cap, speed, series = MEM_SPEC[sku]
        toks += [series, rf"{cap}g", rf"{speed}"]
    elif sku in SSD_CAP:
        cap = SSD_CAP[sku]
        toks += [rf"{cap}tb" if cap < 100 else rf"{cap}g"]
    return toks


def find_spans(text: str, lits: list[str], guarded4: bool) -> list[tuple[int, int]]:
    out = []
    for lit in lits:
        if guarded4 and re.fullmatch(r"\d{4}", lit):
            for m in re.finditer(r"(\d{4})(?!\d)", text):
                if m.group(1) == lit:
                    out.append((m.start(), m.end()))
        else:
            for m in re.finditer(re.escape(lit), text):
                out.append((m.start(), m.end()))
    return out


def cat_lit_spans(cat: str, text: str, target_lits: list[str], cand_tok) -> list[tuple[int, int, bool]]:
    """全部品类 token 的 span;返回 (start, end, is_target)。"""
    tset = set(target_lits)
    others = set(EXTRA_LITS.get(cat, []))
    others |= {t for s, v in TAIL_ID.items() if s.split("-")[0] == cat for t in v}
    others |= cand_tok.get(cat, set())
    others -= tset
    spans = [(s, e, True) for s, e in find_spans(text, sorted(tset), cat == "mem")]
    spans += [(s, e, False) for s, e in find_spans(text, sorted(others), cat == "mem")]
    return spans


def other_variant_hit(text: str, cat: str, target_lits: list[str], cand_tok) -> str | None:
    spans = cat_lit_spans(cat, text, target_lits, cand_tok)
    for s, e, is_t in spans:
        if is_t:
            continue
        if any(ts <= s and e <= te for ts, te, t in spans if t):
            continue
        return text[s:e]
    return None


def core_valid(core: str, zone: str, cat: str, target_lits: list[str], cand_tok) -> bool:
    """core 在 zone 中存在不被其他 token 吞没的匹配。"""
    spans = cat_lit_spans(cat, zone, target_lits, cand_tok)
    core_spans = [(m.start(), m.end()) for m in re.finditer(core, zone)]
    for cs, ce in core_spans:
        swallowed = any(ts <= cs and ce <= te and (te - ts) > (ce - cs)
                        for ts, te, t in spans if not t)
        if not swallowed:
            return True
    return False


def eligible(sku: str, stitle: str) -> tuple[bool, str]:
    for f in FORBS.get(sku, []):
        if re.search(f, stitle):
            return False, f"forb:{f}"
    for r in REQS.get(sku, []):
        if not re.search(r, stitle):
            return False, f"req:{r}"
    if sku in SERIES_SUFFIX and not re.search(SERIES_SUFFIX[sku], suffix_text(sku, stitle)):
        return False, "series_mismatch"
    if sku in MEM_SPEC:
        cap, speed, _ = MEM_SPEC[sku]
        caps = mem_caps(stitle)
        if max(caps, default=0) != cap:
            return False, f"cap{sorted(caps)}!={cap}"
        if speed not in mem_speeds(stitle):
            return False, "speed_missing"
    if sku in MEM_CAP and max(mem_caps(stitle), default=0) != MEM_CAP[sku]:
        return False, "memcap_mismatch"
    if sku in SSD_CAP:
        caps = ssd_caps(stitle)
        if len(caps) != 1 or SSD_CAP[sku] not in caps:
            return False, f"cap{sorted(caps)}"
    return True, ""


def bracket_zone(title: str, z: int) -> str:
    tail = sq(title)[-z:]
    brs = "".join(sq(b) for b in re.findall(r"【([^】]*)】", title))
    return tail + "‖" + brs


def bind_tier(sku: str, cat: str, stitle: str, zone: str, ok_skus: list[str], cand_tok,
              strict: bool) -> str | None:
    target_lits = TAIL_ID.get(sku, [])
    if sku in MEM_SPEC:
        target_lits = target_lits + [str(MEM_SPEC[sku][1])]
    core = tier_a_core(sku)
    # tier A:zone 含全部核心且无其他型号 token
    if all(core_valid(c, zone, cat, target_lits, cand_tok) for c in core):
        hit = other_variant_hit(zone, cat, target_lits, cand_tok)
        if not hit:
            # 非京东(淘宝/拼多多/聚合)多型号链接显示最低档价不可用:尾部绑定不够,需全标题单型号
            if strict and other_variant_hit(stitle, cat, target_lits, cand_tok):
                return None
            return "A"
        return None
    # tier B:单型号 listing(全标题无其他型号 token)且守卫唯一
    if len(ok_skus) != 1:
        return None
    hit = other_variant_hit(stitle, cat, target_lits, cand_tok)
    if not hit:
        return "B"
    return None


def suffix_text(sku: str, stitle: str) -> str:
    """选中变体后缀:身份 token 最后一次出现之后的文本。"""
    lits = list(TAIL_ID.get(sku, []))
    if sku in MEM_SPEC:
        lits += [str(MEM_SPEC[sku][1])]
    end = 0
    for lit in lits:
        for m in re.finditer(re.escape(lit), stitle):
            end = max(end, m.end())
    return stitle[end:] if end else ""


def suffix_tray(sku: str, stitle: str) -> bool:
    """散片判定看选中变体(身份 token 最后一次出现之后)的文本,而非全标题。"""
    sfx = suffix_text(sku, stitle)
    if not sfx:
        return bool(TRAY.search(stitle))
    return bool(re.search(r"散片|散装", sfx[:12]))


def load_prev() -> dict[str, dict]:
    with PREV_CSV.open(encoding="utf-8") as f:
        return {r["sku"]: r for r in csv.DictReader(f)}


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--emit", action="store_true")
    ap.add_argument("--sku", action="append", default=[], help="调试:打印指定 SKU 的全部候选行")
    ap.add_argument("--hits", default=None, help="覆盖 HITS 输入路径")
    ap.add_argument("--prev", default=None, help="覆盖 PREV_CSV 沿用基线")
    ap.add_argument("--out-csv", default=None, help="覆盖 OUT_CSV 输出")
    ap.add_argument("--out-decisions", default=None, help="覆盖 OUT_DECISIONS 输出")
    args = ap.parse_args()
    global HITS, PREV_CSV, OUT_CSV, OUT_DECISIONS
    if args.hits:
        HITS = Path(args.hits)
    if args.prev:
        PREV_CSV = Path(args.prev)
    if args.out_csv:
        OUT_CSV = Path(args.out_csv)
    if args.out_decisions:
        OUT_DECISIONS = Path(args.out_decisions)

    rows = [json.loads(l) for l in HITS.read_text(encoding="utf-8").splitlines() if l.strip()]
    prev = load_prev()
    cand_tok = candidate_tokens()

    per_sku: dict[str, list[dict]] = defaultdict(list)
    seen_logical: set[tuple] = set()
    stats = defaultdict(int)
    guard_fails = defaultdict(int)
    for row in rows:
        price = row.get("price_cny")
        if not price:
            stats["no_price"] += 1
            continue
        stitle = sq(row["title"])
        if any(sq(j) in stitle for j in JUNK):
            stats["junk"] += 1
            continue
        lp = (price, stitle)
        if lp in seen_logical:
            stats["dup_logical"] += 1
            continue
        seen_logical.add(lp)
        cat = row["category"]
        zone = bracket_zone(row["title"], ZONE_MEM if cat == "mem" else ZONE)
        hits_skus = [s for s, pat in LEGACY_PATTERNS.items()
                     if s.split("-")[0] == cat and re.search(pat, stitle)]
        ok_skus, fail_reasons = [], []
        for s in hits_skus:
            ok, why = eligible(s, stitle)
            if ok:
                ok_skus.append(s)
            else:
                fail_reasons.append(f"{s}:{why}")
        if not ok_skus and fail_reasons:
            for why in fail_reasons:
                guard_fails[why.split(":")[-1]] += 1
        for s in ok_skus:
            strict = str(row.get("platform_src") or "") != "2"
            tier = bind_tier(s, cat, stitle, zone, ok_skus, cand_tok, strict)
            stats[f"tier_{tier or 'none'}"] += 1
            if not tier:
                continue
            per_sku[s].append({
                "row_key": row["row_key"], "title": row["title"], "price": price,
                "shop": row.get("shop"), "platform": row.get("platform_src"),
                "tier": tier, "tray": suffix_tray(s, stitle),
            })

    decisions = []
    for sku in sorted(LEGACY_PATTERNS):
        old = prev.get(sku)
        old_price = float(old["price_cny"]) if old else None
        cands = per_sku.get(sku, [])
        if not cands:
            decisions.append({"sku": sku, "decision": "carry", "reason": "no_hit",
                              "old_price": old_price, "new_price": None, "evidence": []})
            continue
        if args.sku and sku in args.sku:
            print(f"\n== {sku} 全部候选 ==")
            for c in sorted(cands, key=lambda x: x["price"]):
                print(f"  [{c['tier']}] {c['price']:>9.2f} tray={c['tray']} p={c['platform']} "
                      f"{c['row_key']} {c['title']}")
        boxed = [c for c in cands if not c["tray"]]
        pool, tray_note = (boxed, "") if boxed else (cands, "仅散片" if cands[0]["tray"] else "")
        best = min(pool, key=lambda c: c["price"])
        status, delta = "accept", None
        if old_price:
            delta = best["price"] / old_price
            if delta < SANITY_LO or delta > SANITY_HI:
                status = "review"
        decisions.append({
            "sku": sku, "decision": status, "reason": tray_note or f"tier_{best['tier']}",
            "old_price": old_price, "new_price": best["price"],
            "delta": round(delta, 3) if delta else None,
            "evidence": sorted(pool, key=lambda c: c["price"])[:5],
        })

    print(f"{'sku':38} {'决定':7} {'旧':>9} {'新':>9} {'比':>6} 证据")
    for d in decisions:
        if d["decision"] == "carry" and d["reason"] == "no_hit":
            continue
        ev = d["evidence"][0] if d["evidence"] else {}
        print(f"{d['sku']:38} {d['decision']:7} "
              f"{d['old_price'] or 0:9.2f} {d['new_price'] or 0:9.2f} "
              f"{(d['delta'] or 0):6.2f} [{ev.get('tier','?')}] {ev.get('title','')[:52]}")
    acc = sum(1 for d in decisions if d["decision"] == "accept")
    rev = sum(1 for d in decisions if d["decision"] == "review")
    car = sum(1 for d in decisions if d["decision"] == "carry")
    print(f"\nSKU 统计: accept {acc} / review {rev} / carry {car};行级 {dict(stats)}")
    print("守卫失败 Top:", sorted(guard_fails.items(), key=lambda x: -x[1])[:10])

    if not args.emit:
        return 0

    lines = ["sku,price_cny,source,captured_at"]
    for sku in sorted(LEGACY_PATTERNS):
        d = next(x for x in decisions if x["sku"] == sku)
        if d["decision"] == "accept":
            lines.append(f"{sku},{d['new_price']:.2f},{SOURCE},{CAPTURED_AT}")
        else:
            o = prev[sku]
            lines.append(f"{sku},{float(o['price_cny']):.2f},{o['source']},{o['captured_at']}")
    OUT_CSV.write_text("\n".join(lines) + "\n", encoding="utf-8")
    OUT_DECISIONS.write_text(
        "\n".join(json.dumps(d, ensure_ascii=False) for d in decisions) + "\n", encoding="utf-8")
    print(f"\n写出 {OUT_CSV} 与 {OUT_DECISIONS}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
