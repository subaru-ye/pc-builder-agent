# -*- coding: utf-8 -*-
"""把人工复核决策(Agent 辅助复核)应用到 154 个候选并生成交付物/详情清单。

复核规则(与 review_draft.md 一致):
- 变体绑定原则:电商多型号列表的价格只绑定尾部区段(约末16个 sq 字符)或【】内的型号,
  标题其他位置出现型号 token 不绑定价格。
- 清洗后仍无绑定行的模型 → reject(不猜造);绑定冲突(如 B570 标 8GB) → reject。
- 价格异常仅作 flag,不作为拒绝新模型的理由(提示词规定)。

用法:
  python finalize_candidates.py decisions   # 生成 review-decisions.jsonl + detail_ids.jsonl
  python finalize_candidates.py emit        # 详情跑完后生成交付物(与 detail YAML 合并)
"""
from __future__ import annotations

import hashlib
import json
import re
import shutil
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
RAW = HERE.parents[3] / "var/data/collections/2026-09-13-maishou-expansion-01"
OUT = HERE.parents[3] / "scripts/data/catalog-candidates/2026-09-13"

# model_id -> (verdict, note, exclude[(price, shop子串)])
# verdict: accept_candidate / reject / review(待复核,本轮不交付)
D: dict[str, tuple[str, str, list]] = {}

def a(mid, note="", ex=None):
    D[mid] = ("accept_candidate", note, ex or [])

def r(mid, note):
    D[mid] = ("reject", note, [])

def v(mid, note):
    D[mid] = ("review", note, [])

# ---------------- CPU ----------------
a("cpu-i3-14100f", "多行绑定14100F,盒装")
a("cpu-i5-12490f", "盒装绑定12490F")
a("cpu-i5-13490f", "绑定13490F")
a("cpu-i5-13600kf", "绑定13600KF")
a("cpu-i5-14400f", "盒装绑定14400F")
a("cpu-i7-12700f", "1450华智单型号行+1650散片绑定行", [(420, "CPU批发商"), (510, "扬员"), (585, "麟隆轩"), (890, "蕊德")])
a("cpu-i7-13700f", "绑定13700F")
a("cpu-ultra7-265k", "1865讯驰绑定Ultra7 265K全新盒装", [(999, "英特尔集宇"), (1981, "瑞达中腾"), (2379, "蜀刀"), (2489, "蜀刀"), (2999, "蜀刀"), (3899, "天擎"), (5459, "ROG旗舰店"), (8599, "华硕"), (8999, "华硕")])
a("cpu-ultra9-285k", "intel官方店绑定285K")
a("cpu-r5-4500", "绑定4500")
a("cpu-r5-5600gt", "AMD官方店绑定5600GT")
a("cpu-r5-5700x3d", "尾部绑定5700X3D散片/盒装")
a("cpu-r7-5800x3d", "AMD官方+自营多店绑定5800X3D盒装2499")
a("cpu-r5-8400f", "绑定8400F")
a("cpu-r7-8700f", "绑定8700F")
a("cpu-r7-8700g", "绑定8700G")
a("cpu-r9-7900x", "绑定7900X")
a("cpu-r9-9950x3d", "绑定9950X3D散片/盒装4536-4824;1039行疑似占位价剔除", [(1039, "电竞DIY"), (1039, "1号电竞")])
r("cpu-ultra5-225k", "无任何绑定行")
r("cpu-r5-5600g", "命中行全为拆机/散片垃圾词")

# ---------------- GPU ----------------
a("gpu-rtx5050-colorful", "官方店绑定5050")
a("gpu-rtx5050-zotac", "绑定5050")
a("gpu-rtx5050-py", "映众官方绑定RTX5050 X2B 8G")
a("gpu-rtx5050-asus", "华硕官方绑定RTX5050 8G 雪豹")
r("gpu-rtx5050-maxsun", "无绑定行")
a("gpu-rtx3050-8g-colorful", "绑定3050 8G")
a("gpu-rtx3050-8g-gigabyte", "绑定3050 8G")
a("gpu-rtx3050-8g-yeston", "绑定3050 8G")
a("gpu-arc-a580-gunnir", "正扬多型号列表尾部绑定A580 8G Index(手动新增)")
a("gpu-arc-a750-gunnir", "蓝戟官方绑定A750 Photon 8G OC;正扬Index 1389作第二offer")
a("gpu-arc-a750-sparkle", "绑定A750")
a("gpu-arc-a770-8g-gunnir", "正扬列表尾部绑定A770 8G Photon(手动新增)")
a("gpu-arc-a770-16g-gunnir", "正扬列表尾部绑定A770 16G Photon(手动新增)")
r("gpu-arc-a770-gunnir", "拆分为8G/16G两条候选")
a("gpu-arc-b570-gunnir", "正扬尾部绑定B570 10G Index/Photon")
a("gpu-arc-b570-sparkle", "绑定B570")
r("gpu-arc-b570-acer", "B570应为10GB,标题写8GB,身份冲突不猜造")
a("gpu-rx7600xt-asrock", "绑定7600XT 16G")
a("gpu-rx7800xt-xfx", "讯景官方绑定7800XT 16G 黑狼(round2)")
r("gpu-rx7900gre-xfx", "无绑定行")
a("gpu-rx9070-sapphire", "蓝宝石官方绑定RX9070 极地 16G OC")
a("gpu-rtx5060-colorful", "官方绑定5060")
a("gpu-rtx5060ti8g-msi", "绑定5060Ti 8G")
a("gpu-rtx5060ti8g-galax", "绑定5060Ti 8G;价格偏高作flag")
a("gpu-rtx5060ti8g-yeston", "绑定5060Ti 8G")
r("gpu-rtx5060ti8g-zotac", "唯一行为映众/索泰/盈通混合列表,绑定冰龙非索泰")
a("gpu-rtx5060ti16g-colorful", "官方绑定5060Ti 16G")
a("gpu-rtx5070-galax", "绑定5070;价格偏高作flag(新模型不作拒收)")
a("gpu-rtx5070ti-zotac", "绑定5070Ti")
a("gpu-rtx5070ti-gigabyte", "绑定5070Ti")
a("gpu-rtx5070ti-asus", "绑定5070Ti")
r("gpu-rtx5080-msi", "命中行为水冷头/配件")
r("gpu-rtx4060-colorful", "850元显著低于市场,疑似占位/二手,不交付")
r("gpu-rx9060xt8g-colorful", "无绑定行")
r("gpu-rx9060xt8g-xfx", "无绑定行")

# ---------------- Motherboard ----------------
a("mb-asus-tuf-b760m-plus-wifi2-d5", "649 ROG旗舰店绑定D5;379行未绑定剔除", [(379, "")])
a("mb-asus-tuf-b850m-plus-wifi7", "官方店绑定B850M-PLUS")
a("mb-asus-b850m-e-tuf", "官方店绑定B850M-E")
a("mb-asus-prime-h610m-k", "京东自营绑定H610M-K")
a("mb-asus-prime-h610m-a", "绑定H610M-A")
a("mb-asus-rog-b760i", "绑定ROG STRIX B760-I")
a("mb-asus-tuf-x870-plus", "绑定X870-PLUS")
a("mb-gb-b760m-aorus-elite-d4", "绑定B760M AORUS ELITE DDR4")
a("mb-gb-b760m-aorus-elite-d5", "绑定B760M AORUS ELITE DDR5")
a("mb-gb-x870-aorus-elite", "绑定X870 AORUS ELITE")
a("mb-gb-x870e-aorus-elite", "1760绑定行;1299未绑定剔除", [(1299, "")])
a("mb-msi-b850m-mortar-wifi", "官方店绑定B850M MORTAR WIFI")
a("mb-msi-b850m-edge-ti", "官方店绑定B850M EDGE TI")
a("mb-msi-b760m-mortar-wifi2-d5", "官方店绑定D5")
a("mb-msi-b550m-mortar-wifi", "绑定B550M MORTAR")
a("mb-msi-b650i-edge", "绑定B650I EDGE")
a("mb-msi-x870e-tomahawk", "绑定X870E TOMAHAWK")
a("mb-asrock-b760m-proa-d5", "绑定B760M PRO A/D5")
a("mb-asrock-b850m-pro-rs", "绑定B850M PRO RS")
a("mb-maxsun-b650i-ice", "绑定B650I 冰霜")
a("mb-jginyu-b650i-nightdevil", "绑定B650I 暗夜")
r("mb-asus-tuf-b760m-plus-wifi2-d4", "无绑定行")
r("mb-msi-b760m-mortar-d4", "绑定行尾部均指向PRO B760M-E/BOMBER")
r("mb-msi-b650m-gaming-plus-wifi", "无绑定行")
r("mb-asrock-b850m-steel-legend", "无绑定行")
r("mb-gb-b850m-gaming-ice", "无绑定行")

# ---------------- Memory ----------------
a("mem-kingbank-silver-ddr5-16-6000", "【16G 8G×2】海力士M-C36绑定1779;另见同款369行(疑似旧价/占位)作flag;1799行32G矛盾剔除", [(1799, "极速纪元")])
a("mem-klevv-fitv-ddr5-32-6000", "完整型号绑定FIT V 32G 6000 C28")
a("mem-klevv-urbane-ddr5-32-6000", "炎龙=URBANE V RGB合并;排除雷霆V行", [(3729, "汇智蓝海")])
a("mem-acer-iceblade-ddr5-64-6000", "64G(32G*2)6000C32显式绑定;1699/2099偏低作flag", [(3199, "宽澜")])
a("mem-adata-ddr5-32-6000", "官方店绑定32GB 2条套条;2299/2429为16G单条剔除", [(2299, "威刚"), (2429, "威刚")])
a("mem-kingbank-xingren-48-6000", "官方店绑定星刃48G(24G*2)6000RGB C28;3246为24G单条剔除;由ddr5-24-6800改名", [(3246, "榜单精选")])
r("mem-kingbank-ddr5-24-6800", "改名为 mem-kingbank-xingren-48-6000(标题证据为6000RGB,非6800马甲)")
a("mem-gloway-tiance-ddr5-32-6000", "恒信绑定天策一代16GX2 6000C36;其余行5600/4800未绑定")
r("mem-kingbank-silver-ddr5-32-6000", "无绑定行")
r("mem-kingbank-ddr5-48-6800", "行均绑定48G(24G*2)套条,96G无证据")
r("mem-kingbank-silver-ddr4-8-3200", "唯一行银爵/黑爵关键词混杂且高于常态价")
r("mem-klevv-blaze-ddr5-32-6000", "与URBANE V RGB为同一产品,合并")
r("mem-jiuhe-xingyu-ddr5-32-6000", "无绑定行")
r("mem-acer-hera-ddr5-32-6000", "round2复搜仍无绑定行")
r("mem-gloway-tiance-ddr4-32-3200", "无绑定行")
r("mem-gloway-longwu-ddr5-32-6800", "无绑定行")
r("mem-crucial-ddr5-32-5600", "无绑定行")
r("mem-kingbank-star-ddr5-16-6800", "命中行绑定银爵6000/星刃48G,非本型号")

# ---------------- SSD ----------------
a("ssd-zhitai-tiplus5000-1tb", "1081/1099绑定TiPlus5000 1T;888行绑定Ti600剔除", [(888, "捷存")])
a("ssd-zhitai-ti600-1tb", "679绑定Ti600 1TB;969行Ti600/GM7混合剔除")
a("ssd-zhitai-ti600-2tb", "1779绑定【2TB】;其余行为混合列表/异常低价剔除", [(649, "精战"), (679, "ASUS华硕"), (679, "星欣达"), (876, "安防科技"), (920, "山己几白王")])
a("ssd-zhitai-tiplus7100-1tb", "致态自营绑定1TB")
a("ssd-zhitai-tiplus7100-2tb", "1929/1999绑定【2TB】;1129混合行剔除", [(1129, "电脑配件")])
a("ssd-zhitai-tiplus9100-1tb", "1888尾部绑定TiPlus9100 1TB;1079行绑定2TB/4T剔除", [(1079, "电码驿站")])
a("ssd-kingston-nv3-1tb", "641.75/685绑定NV3 1TB;570行为SA400混合剔除", [(570, "华东电脑")])
a("ssd-kingston-nv3-2tb", "1829绑定NV3 2T;999为2230规格另一产品;1199混合剔除", [(999, "荣鑫达"), (1199, "kingston金士顿")])
a("ssd-kingston-nv3-500g", "689/859绑定500G;500G高于1TB为市场现象作flag")
a("ssd-lexar-nm610pro-1tb", "纳克行尾部绑定NM610PRO 1TB 839(手动新增)")
r("ssd-lexar-nm620-1tb", "命中行均绑定NM610PRO或其他型号")
a("ssd-lexar-nm790-1tb", "1039绑定NM790 1TB;1328混合行剔除", [(1328, "玩家国度")])
a("ssd-wd-sn5000-1tb", "1199/1399/1699绑定SN5000 1TB")
a("ssd-wd-sn7100-1tb", "1349/1399绑定SN7100 1TB(round2)")
a("ssd-wd-sn7100-500g", "859官方绑定SN7100 500G(手动新增)")
r("ssd-wd-sn770-500g", "所有行绑定SN7100/SN5100/SN770M,SN770 500G无证据")
a("ssd-samsung-990evoplus-1tb", "1230/1499/1503绑定990EVO Plus 1TB")
r("ssd-samsung-990evoplus-2tb", "无绑定行")
a("ssd-crucial-p310-1tb", "1249/1272/1569绑定P310 1TB")
a("ssd-crucial-p510-1tb", "1019绑定P510 1TB(工包,单店)")

# ---------------- PSU ----------------
a("psu-huntkey-wd650evo", "269赞达绑定WD650EVO炫金战神650W金牌;299宽澜标铜牌,认证存在矛盾记录在缺项;169/209/269航舰未绑定剔除", [(169, "微航"), (209, "航嘉官方"), (269, "兆方"), (269, "航舰")])
r("psu-segotep-kunlun-gr850", "唯一行GR850W与750W自相矛盾")
r("psu-segotep-gm750w-ice", "冰山版系列标题混杂全瓦数,750W无法绑定")
a("psu-sama-gold-650", "玄武金牌650W V3版绑定;由sama-gold-650改名")
a("psu-sama-gt850", "379先马官方尾部绑定GT850黑色金牌全模组")
r("psu-fsp-hv-vitagpro-650", "标题混杂650-1000W,无法绑定650W")
a("psu-superflower-zillion2-650", "三店429绑定卓凌II 650W,含振华官方")
r("psu-greatwall-gx850", "无绑定行")
r("psu-greatwall-g7-750", "标题混杂650/750/850,750W无法绑定")
a("psu-antec-ne750", "529尾部绑定NE750W ATX3.1/PCIE5.1;439行绑定NE650/TiTAN剔除", [(439, "希博"), (454, "天擎"), (479, "希博"), (519, "希博")])

# ---------------- Case ----------------
a("case-sama-pingtouge-m2", "99/99.9绑定平头哥M2网孔")
a("case-sama-pingtouge-m1", "109绑定平头哥M1小机箱(round2);其余行为侧板/盖板配件剔除", [(48, "塑胶批发"), (50, "庄承"), (50, "猪麦麦"), (95, "奕嗨昆比"), (97.5, "千川"), (337.81, "味原")])
a("case-sama-pingtouge-m9", "129先马官方绑定平头哥M9;M9E/M9 LITE同价变体一并记录;129.88包装箱剔除", [(129.88, "石头洲")])
a("case-sama-quzao3", "125/139绑定趣造3(round2发现;原趣造无绑定行,拆分)")
r("case-sama-quzao", "命中行为配件/模组线/机箱包,趣造本体无绑定行")
a("case-jonsbo-u4-mini", "299官方+自营绑定U4 Mini;由jonsbo-u4改名;168.99包装箱剔除", [(168.99, "石头洲")])
r("case-jonsbo-u4", "改名为 case-jonsbo-u4-mini(命中行绑定U4 Mini)")
a("case-jonsbo-d41-mesh", "329三敢专卖店尾部绑定D41 MESH版网孔白色(round2发现;D31本体无绑定行)")
r("case-jonsbo-d31-mesh", "42行全为收纳包/模组线/灯板/UV打印等配件,D31机箱本体无绑定行")
a("case-aigo-yogo-m2", "159绑定YOGO M2侧透240水冷;45/58行为侧板/防尘罩剔除", [(45, "那个男人"), (58, "波若")])
a("case-coolermaster-q300l-v2", "219官方绑定Q300L V2;416绑定Q300L V2;530混合剔除", [(530, "杭州宋城坊")])
r("case-coolermaster-nr400", "唯一行NR400/NR600双型号未绑定")
a("case-lianli-lancool-207", "513.29/549绑定LANCOOL 207海景房,含联力官方")
r("case-lianli-lancool-205", "唯一行为RGB灯条配件")
a("case-nzxt-h3-flow", "578.55/585.2/681.15绑定H3 Flow(天猫国际直邮)")
a("case-fractal-pop-mini-air", "699官方绑定Pop Mini Air;649行为Silent款拆分", [(649, "分形工艺")])
a("case-fractal-pop-mini-silent", "649官方绑定Pop Mini Silent(由pop-mini拆分)")
r("case-fractal-pop-mini", "拆分为 pop-mini-air / pop-mini-silent 两条候选")
a("case-asus-tuf-gt301", "499×3绑定GT301火枪手")

# ---------------- Cooler ----------------
a("cooler-thermalright-ax120r-se", "多店绑定AX120 R SE")
a("cooler-deepcool-xuanbing400-v5", "多店绑定玄冰400V5,含官方")
a("cooler-deepcool-ak400", "159×2绑定AK400白色;96.6行绑定玄冰400i剔除", [(96.6, "盛京达")])
r("cooler-deepcool-ak500s", "各行为AK400/AK500S/AK620混系列表,无绑定")
a("cooler-idcooling-se214xt-v2", "3店绑定SE-214-XT V2 ARGB")
a("cooler-thermalright-warframe-240", "利民专卖店绑定WARFRAME 240;含无风扇版变体,单店flag")
r("cooler-thermalright-warframe-360", "无绑定行")
a("cooler-valkyrie-a360", "睿琏行尾部绑定A360黑色/白色水冷(手动新增;E360无绑定)")
r("cooler-valkyrie-e360", "命中行尾部均绑定A360;E360无绑定行,拆出 cooler-valkyrie-a360")
r("cooler-valkyrie-e240", "无绑定行")
r("cooler-valkyrie-n360", "无绑定行")
a("cooler-noctua-nh-l9i", "多店绑定NH-L9i")
r("cooler-noctua-nh-l9a", "无绑定行")
a("cooler-thermalright-axp90-x36", "107.8/127.05绑定AXP90-X36;90行为二手出物剔除", [(90, "新颖工控")])
r("cooler-thermalright-axp90-x47", "无绑定行")
r("cooler-thermalright-axp90-x53", "无绑定行")
a("cooler-pccooler-donghai-x7", "119官方绑定东海X7炫彩版黑色")

# 手动新增候选(不在 154 模型表内):从 pool_for_review 按行取
MANUAL = [
    ("gpu-arc-a580-gunnir", "gpu", "蓝戟 Arc A580 8G Index", [(1339.0, "正扬电脑配件专营店")]),
    ("gpu-arc-a770-8g-gunnir", "gpu", "蓝戟 Arc A770 8G Photon", [(1989.0, "正扬电脑配件专营店"), (1889.0, "正扬电脑配件专营店")]),
    ("gpu-arc-a770-16g-gunnir", "gpu", "蓝戟 Arc A770 16G Photon", [(1939.0, "正扬电脑配件专营店"), (1989.0, "正扬电脑配件专营店")]),
    ("ssd-lexar-nm610pro-1tb", "ssd", "雷克沙 NM610 PRO 1TB", [(839.0, "纳克NUC小店")]),
    ("ssd-wd-sn7100-500g", "ssd", "WD Black SN7100 500G", [(859.0, "瑞达中腾官方旗舰店"), (929.0, "西部数据茗玥专卖店")]),
    ("cooler-valkyrie-a360", "cooler", "瓦尔基里 A360 ARGB 一体式水冷", [(399.0, "睿琏电脑配件专营店"), (409.0, "睿琏电脑配件专营店")]),
    ("case-jonsbo-d41-mesh", "case", "乔思伯 D41 MESH 网孔 白色", [(329.0, "乔思伯 JONSBO三敢专卖店")]),
    ("case-sama-quzao3", "case", "先马 趣造3 便携 MATX/ITX", [(125.0, "智睿科技"), (139.0, "先马商宝专卖店")]),
    ("case-fractal-pop-mini-air", "case", "分形工艺 Pop Mini Air RGB", [(699.0, "分形工艺官方旗舰店")]),
    ("case-fractal-pop-mini-silent", "case", "分形工艺 Pop Mini Silent", [(649.0, "分形工艺官方旗舰店")]),
    ("mem-kingbank-xingren-48-6000", "memory", "金百达 星刃 DDR5-6000 48G(2x24G) RGB C28", [(3499.0, "金百达（KINGBANK）旗舰店"), (4079.0, "瑞达中腾官方旗舰店")]),
]

DETAIL_CATS = {"cpu", "gpu", "motherboard", "ssd"}


def load_draft():
    return [json.loads(l) for l in (RAW / "candidates_draft.jsonl").read_text(encoding="utf-8").splitlines() if l.strip()]


def load_pool():
    return [json.loads(l) for l in (RAW / "pool_for_review.jsonl").read_text(encoding="utf-8").splitlines() if l.strip()]


def keep_offers(model, offers):
    ex = D[model][2] if model in D else []
    kept = []
    for o in offers:
        if any(abs(float(o["price_cny"]) - p) < 0.01 and sub in (o.get("shop") or "") for p, sub in ex):
            continue
        kept.append(o)
    return kept


def sq(s: str) -> str:
    s = str(s or "").lower()
    s = re.sub(r"g\s*[x×*]\s*(\d)", r"g\1", s)
    return re.sub(r"[^a-z0-9\u4e00-\u9fff]", "", s)


def extract_specs(cat, name, titles):
    """只提取标题中可见的规格字段;提取不到就不产键(缺失项进 missing-specs)。"""
    t = " ".join(titles).lower()
    tsq = sq(t)
    sp: dict = {}
    if cat == "cpu":
        if re.search(r"盒装", t):
            sp["form"] = "盒装"
        elif re.search(r"散片", t):
            sp["form"] = "散片"
    elif cat == "gpu":
        m = re.search(r"(\d{1,2})\s*g(?:b)?\b", t)
        if m and re.search(r"显存|gddr|8g|16g|10g|12g", t):
            sp["mem_gb"] = int(m.group(1))
    elif cat == "motherboard":
        if re.search(r"ddr4|d4\b", t) and not re.search(r"ddr5", t):
            sp["ddr"] = "DDR4"
        if re.search(r"ddr5", t):
            sp["ddr"] = "DDR5"
        for k, pat in [("wifi7", r"wifi7|wi-fi7|be\d{4}"), ("wifi6e", r"wifi6e|wi-fi6e"), ("wifi", r"wifi|wi-fi|无线")]:
            if re.search(pat, t):
                sp["wifi"] = k
                break
        for k, pat in [("ITX", r"itx|min[- ]itx"), ("MATX", r"matx|m[- ]atx|micro[- ]atx"), ("ATX", r"atx")]:
            if re.search(pat, t):
                sp["form"] = k
                break
    elif cat == "memory":
        if re.search(r"ddr5", t):
            sp["ddr"] = "DDR5"
        elif re.search(r"ddr4", t):
            sp["ddr"] = "DDR4"
        m = re.search(r"(\d{1,2})g\s*[x×*]\s*(\d)", t)
        if m:
            n, kits = int(m.group(1)), int(m.group(2))
            sp["capacity"] = f"{n * kits}G({kits}x{n}G)"
        for speed in re.findall(r"(\d{4})(?!\d)", t):
            if speed in {"3200", "3600", "4800", "5200", "5600", "6000", "6400", "6800", "7200", "8000"}:
                sp["speed_mts"] = int(speed)
                break
        m = re.search(r"c(\d{2})(?!\d)", tsq)
        if m:
            sp["cas"] = f"C{m.group(1)}"
    elif cat == "ssd":
        m = re.search(r"(500\s*gb?|512\s*gb?|1\s*tb?|2\s*tb?|4\s*tb?)", t.replace(" ", ""))
        if m:
            sp["capacity"] = m.group(1).upper().replace(" ", "")
        if re.search(r"pcie\s*5|gen5|pci5", t):
            sp["pcie"] = "5.0"
        elif re.search(r"pcie\s*4|gen4|pci4", t):
            sp["pcie"] = "4.0"
    elif cat == "psu":
        m = re.search(r"(\d{3})\s*w", t)
        if m:
            sp["watt"] = int(m.group(1))
        if re.search(r"atx\s*3\.1", t):
            sp["atx"] = "3.1"
        elif re.search(r"atx\s*3", t):
            sp["atx"] = "3.0"
        if re.search(r"全模组", t):
            sp["modular"] = "全模组"
        for k, pat in [("金牌", r"金牌|80plus\s*gold|gold认证"), ("铜牌", r"铜牌|bronze"), ("白金牌", r"白金牌|platinum")]:
            if re.search(pat, t):
                sp["cert"] = k
                break
    elif cat == "case":
        for k, pat in [("ITX", r"\bitx\b"), ("MATX", r"matx|m[- ]atx|micro[- ]atx"), ("ATX", r"\batx\b")]:
            if re.search(pat, t):
                sp["form"] = k
                break
        if re.search(r"网孔|mesh", t):
            sp["front"] = "网孔"
        if re.search(r"侧透|钢化玻璃|玻璃侧板", t):
            sp["side"] = "侧透"
    elif cat == "cooler":
        if re.search(r"水冷|一体式|aio", t):
            sp["type"] = "一体式水冷"
        elif re.search(r"风冷|散热器", t):
            sp["type"] = "风冷"
        m = re.search(r"(120|240|360)\s*(?:mm)?\s*(?:冷排|水冷)?", t)
        if sp.get("type") == "一体式水冷" and m:
            sp["radiator_mm"] = int(m.group(1))
        m = re.search(r"(\d)\s*热管", t)
        if m:
            sp["heatpipes"] = int(m.group(1))
    return sp


MANUAL_IDS = {x[0] for x in MANUAL}

REQUIRED_FIELDS = {
    "cpu": ["form", "cores", "threads"],
    "gpu": ["mem_gb", "tdp_w"],
    "motherboard": ["ddr", "form", "wifi", "m2_slots"],
    "memory": ["ddr", "capacity", "speed_mts", "cas"],
    "ssd": ["capacity", "pcie", "nand"],
    "psu": ["watt", "cert", "atx", "modular"],
    "case": ["form", "front", "side"],
    "cooler": ["type", "radiator_mm", "heatpipes"],
}

PLATFORM_NAMES = {1: "taobao", 2: "jd", 3: "pdd"}


def get_offers(mid):
    """接受模型的保留 offer(复核剔除后);manual 模型从 pool/legacy pool 取。"""
    if mid in MANUAL_MAP:
        _c, _n, want = MANUAL_MAP[mid]
        got = [
            prow for prow in _ALL_POOL
            if prow["category"] == _c
            and any(abs(float(prow["price_cny"]) - p) < 0.01 and sub in (prow.get("shop") or "") for p, sub in want)
        ]
        seen, uniq = set(), []
        for prow in got:
            k = (prow.get("platform_src"), prow.get("goodsId"), prow["price_cny"])
            if k not in seen:
                seen.add(k)
                uniq.append(prow)
        return uniq
    row = next(r for r in _DRAFT if r["model_id"] == mid)
    return keep_offers(mid, row["offers"])


def parse_detail(path: Path) -> dict:
    t = path.read_text(encoding="utf-8")
    out = {}
    m = re.search(r"商品标题:\s*(.+)", t)
    if m:
        out["detail_title"] = m.group(1).strip()
    m = re.search(r"购买链接:\s*(\S+)", t)
    if m:
        out["buy_url"] = m.group(1).strip()
    m = re.search(r"^\s+shopName:\s*(.+)$", t, re.M)
    if m:
        out["detail_shop"] = m.group(1).strip().strip("'\"")
    m = re.search(r"^\s+actualPrice:\s*'?([\d.]+)'?", t, re.M)
    if m:
        out["detail_price"] = float(m.group(1))
    m = re.search(r"^\s+monthSales:\s*'?(\d+)'?", t, re.M)
    if m:
        out["month_sales"] = int(m.group(1))
    return out


def load_details() -> dict:
    det = {}
    ddir = RAW / "raw" / "detail"
    if not ddir.exists():
        return det
    for f in ddir.glob("*.yaml"):
        mid = f.name.split("__")[0]
        det.setdefault(mid, parse_detail(f))
    return det


def cmd_emit() -> int:
    global _DRAFT, _ALL_POOL, MANUAL_MAP
    MANUAL_MAP = {x[0]: (x[1], x[2], x[3]) for x in MANUAL}
    _DRAFT = load_draft()
    _ALL_POOL = load_pool() + [json.loads(l) for l in (RAW / "legacy_price_hits_pool.jsonl").read_text(encoding="utf-8").splitlines() if l.strip()]
    dec_rows = [json.loads(l) for l in (OUT / "review-decisions.jsonl").read_text(encoding="utf-8").splitlines() if l.strip()]
    details = load_details()

    candidates, offers = [], []
    for rec in dec_rows:
        if rec["verdict"] != "accept_candidate":
            continue
        mid = rec["model_id"]
        kept = get_offers(mid)
        if not kept:
            continue
        kept = sorted(kept, key=lambda o: float(o["price_cny"]))
        titles = [o["title"] for o in kept]
        specs = extract_specs(rec["category"], rec["name"], titles)
        required = list(REQUIRED_FIELDS[rec["category"]])
        if rec["category"] == "cooler":
            required = ["type"] + (["radiator_mm"] if specs.get("type") == "一体式水冷" else ["heatpipes"])
        missing = [f for f in required if f not in specs]
        prices = [float(o["price_cny"]) for o in kept]
        shops = {o.get("shop") or "" for o in kept}
        det = details.get(mid, {})
        row = {
            "model_id": mid, "category": rec["category"], "name": rec["name"],
            "verdict": "accept_candidate",
            "specs": specs, "missing_fields": missing,
            "price_min": min(prices), "price_median": sorted(prices)[len(prices) // 2],
            "n_offers": len(kept), "n_shops": len(shops),
            "platforms": sorted({PLATFORM_NAMES.get(o["platform_src"], str(o["platform_src"])) for o in kept}),
            "note": rec["note"],
            "review_basis": "Agent 辅助复核",
            "buy_url": det.get("buy_url"),
            "detail_price": det.get("detail_price"),
            "detail_checked": mid in details,
        }
        candidates.append(row)
        for i, o in enumerate(kept):
            offers.append({
                "model_id": mid, "category": rec["category"],
                "row_key": o.get("row_key"), "platform_src": o["platform_src"],
                "platform": PLATFORM_NAMES.get(o["platform_src"], str(o["platform_src"])),
                "goodsId": o.get("goodsId"), "title": o["title"],
                "price_cny": o["price_cny"], "shop": o.get("shop"),
                "month_sales": o.get("month_sales"),
                "is_primary": i == 0,
                "buy_url": det.get("buy_url") if i == 0 else None,
            })

    OUT.mkdir(parents=True, exist_ok=True)
    (OUT / "candidates.jsonl").write_text("\n".join(json.dumps(x, ensure_ascii=False) for x in candidates) + "\n", encoding="utf-8")
    (OUT / "offers.jsonl").write_text("\n".join(json.dumps(x, ensure_ascii=False) for x in offers) + "\n", encoding="utf-8")

    # 缺项清单
    lines = ["# 新型号候选缺规格清单 2026-09-13", "",
             "规格仅记录标题/详情页可见字段(不猜造);下表为接受候选中缺失的字段。", ""]
    for c in candidates:
        if c["missing_fields"]:
            lines.append(f"- `{c['model_id']}` {c['name']} 缺: {', '.join(c['missing_fields'])}")
    n_missing = sum(1 for c in candidates if c["missing_fields"])
    lines.insert(4, f"共 {n_missing}/{len(candidates)} 个候选存在缺项(其余字段齐备)。")
    lines.append("")
    (OUT / "missing-specs.md").write_text("\n".join(lines), encoding="utf-8")

    # manifest
    shutil.copyfile(RAW / "legacy_price_hits_pool.jsonl", OUT / "legacy-price-hits.jsonl")
    cp = json.loads((RAW / "checkpoint.json").read_text(encoding="utf-8"))
    files = {}
    for name in ["candidates.jsonl", "offers.jsonl", "review-decisions.jsonl", "missing-specs.md", "legacy-price-hits.jsonl"]:
        p = OUT / name
        files[name] = {"sha256": hashlib.sha256(p.read_bytes()).hexdigest(), "bytes": p.stat().st_size}
    manifest = {
        "batch": "2026-09-13-maishou-expansion-01",
        "date": "2026-09-13",
        "source_type": "aggregator_secondary",
        "review_basis": "Agent 辅助复核",
        "skill": "买手技能(uv run scripts/main.py search/detail)",
        "budget": {"search": f"{cp['search_count']}/200", "detail": f"{cp['detail_count']}/120",
                   "http_requests": cp["http_requests"]},
        "counts": {
            "candidates": len(candidates),
            "offers": len(offers),
            "models_detail_checked": sum(1 for c in candidates if c["detail_checked"]),
            "with_missing_fields": n_missing,
        },
        "files": files,
        "not_imported": "本批仅为候选,未导入产品库/基线;同型号多店铺为多 offer 非多 SKU",
    }
    (OUT / "manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2), encoding="utf-8")

    print(json.dumps(manifest["counts"], ensure_ascii=False))
    from collections import Counter
    print(Counter(c["category"] for c in candidates))
    return 0


def cmd_decisions() -> int:
    draft = load_draft()
    pool = load_pool()
    legacy_pool = [json.loads(l) for l in (RAW / "legacy_price_hits_pool.jsonl").read_text(encoding="utf-8").splitlines() if l.strip()]
    all_pool = pool + legacy_pool
    by_model = {r["model_id"]: r for r in draft}

    dec_rows = []
    detail_ids = []
    accepted = {}

    for row in draft:
        mid = row["model_id"]
        if mid in MANUAL_IDS:
            continue
        verdict, note, _ex = D.get(mid, ("review", "未复核", []))
        rec = {
            "model_id": mid, "category": row["category"], "name": row["name"],
            "verdict": verdict, "note": note,
            "n_offers": row["n_offers"], "flags": row["flags"],
            "review_basis": "Agent 辅助复核(变体绑定+身份去重)",
        }
        if verdict == "accept_candidate":
            kept = keep_offers(mid, row["offers"])
            if not kept:
                rec["verdict"] = "reject"
                rec["note"] += ";剔除绑定失败行后无保留行"
            else:
                rec["kept_offers"] = len(kept)
                accepted[mid] = (row, kept)
        dec_rows.append(rec)

    for mid, cat, name, want in MANUAL:
        got = [
            prow for prow in all_pool
            if prow["category"] == cat
            and any(abs(float(prow["price_cny"]) - p) < 0.01 and sub in (prow.get("shop") or "") for p, sub in want)
        ]
        # 去重(同 goodsId 保留一条)
        seen = set()
        uniq = []
        for prow in got:
            k = (prow.get("platform_src"), prow.get("goodsId"), prow["price_cny"])
            if k in seen:
                continue
            seen.add(k)
            uniq.append(prow)
        if not uniq:
            print(f"[warn] manual {mid} 未匹配到 pool 行")
            continue
        offers = [{
            "price_cny": p["price_cny"], "platform_src": p["platform_src"],
            "goodsId": p.get("goodsId"), "shop": p.get("shop"), "title": p["title"],
            "row_key": p.get("row_key"),
        } for p in uniq]
        accepted[mid] = ({"model_id": mid, "category": cat, "name": name}, offers)
        dec_rows.append({
            "model_id": mid, "category": cat, "name": name,
            "verdict": "accept_candidate", "note": D[mid][1],
            "n_offers": len(offers), "flags": [], "kept_offers": len(offers),
            "review_basis": "Agent 辅助复核(变体绑定+身份去重)",
        })

    for mid, (row, kept) in accepted.items():
        if row["category"] not in DETAIL_CATS:
            continue
        primary = sorted(kept, key=lambda o: float(o["price_cny"]))[0]
        if primary.get("goodsId"):
            detail_ids.append({
                "detail_id": f"{mid}__{primary.get('row_key') or primary.get('goodsId')}",
                "model_id": mid,
                "source": int(primary["platform_src"]),
                "goodsId": primary["goodsId"],
                "price_cny": primary["price_cny"],
                "shop": primary.get("shop"),
            })

    OUT.mkdir(parents=True, exist_ok=True)
    (OUT / "review-decisions.jsonl").write_text(
        "\n".join(json.dumps(x, ensure_ascii=False) for x in dec_rows) + "\n", encoding="utf-8")
    (RAW / "detail_ids.jsonl").write_text(
        "\n".join(json.dumps(x, ensure_ascii=False) for x in detail_ids) + "\n", encoding="utf-8")
    n_acc = sum(1 for x in dec_rows if x["verdict"] == "accept_candidate")
    n_rej = sum(1 for x in dec_rows if x["verdict"] == "reject")
    n_rev = sum(1 for x in dec_rows if x["verdict"] == "review")
    print(json.dumps({"models": len(dec_rows), "accept": n_acc, "reject": n_rej, "review": n_rev,
                      "detail_ids": len(detail_ids)}, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    cmd = sys.argv[1] if len(sys.argv) > 1 else "decisions"
    if cmd == "decisions":
        sys.exit(cmd_decisions())
    if cmd == "emit":
        sys.exit(cmd_emit())
    sys.exit(1)
