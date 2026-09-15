"""Read-only intake audit; never promotes candidates or changes collected rows."""
import json
import argparse
import hashlib
import subprocess
import sys
from collections import Counter
from decimal import Decimal
from pathlib import Path

ROOT = Path(__file__).resolve().parents[4]
sys.path.insert(0, str(ROOT / "scripts/data/src"))
from pcdata.canonical import SPEC_FIELDS, validate_specs
from pcdata.coverage import _required_fields

SOURCE = ROOT / "scripts/data/catalog-candidates/2026-09-13"

# Explicit, reviewable corrections to this immutable intake batch. A quoted
# fragment must still occur in the selected offer; no fuzzy repair is applied.
CORRECTIONS = {
    "cpu-i3-14100f": ("i3 14100F 全新散片", {"form":"散片"}),
    "cpu-i5-13600kf": ("i5-13600KF散片", {"form":"散片"}),
    "cpu-i5-14400f": ("i5 14400F 全新散片", {"form":"散片"}),
    "cpu-i7-13700f": ("i7 13700F散片", {"form":"散片"}),
    "cpu-i7-12700f": ("英特尔 I7 12700F CPU", {"form":None}),
    "cpu-r5-4500": ("R5 4500【全新散片】", {"form":"散片"}),
    "cpu-r5-5700x3d": ("R7 5700X3D 全新散片", {"form":"散片"}),
    "cpu-r5-8400f": ("R5 8400F 【全新散片】", {"form":"散片"}),
    "cpu-r7-8700f": ("R7 8700F散片", {"form":"散片"}),
    "cpu-r7-8700g": ("R7 8700G散片", {"form":"散片"}),
    "cpu-r9-9950x3d": ("R9 9950X3D【全新散片】", {"form":"散片"}),
    "gpu-rtx5070ti-zotac": ("RTX5070Ti 16G X-GAMING黑OC", {"mem_gb":16}),
    "ssd-zhitai-tiplus9100-1tb": ("TiPlus9100 1TB", {"capacity":"1TB"}),
    "cooler-valkyrie-a360": ("A360 ARGB黑色水冷 单水冷", {"radiator_mm":None}),
    "case-sama-pingtouge-m2": ("不侧透机箱", {"side":"不侧透"}),
    "case-fractal-pop-mini-silent": ("Pop Mini Silent 黑色(非侧透)", {"side":"不侧透"}),
    "case-jonsbo-d41-mesh": ("D41 MESH版 网孔 白色 ATX主板", {"form":"ATX"}),
}
IDENTITY_REVIEW = {
    "cpu-ultra9-285k":"多型号 285K/265KF/270K 同列，没有选中变体证据",
    "gpu-rtx3050-8g-colorful":"选中尾部为华硕雪豹，不能绑定七彩虹",
    "gpu-rtx5060ti16g-colorful":"同一标题同时含16G和8G，没有容量选择证据",
    "gpu-rtx5070ti-gigabyte":"多个板卡系列，所选风魔/SFF需要精确后缀及去重核对",
    "mb-asus-tuf-b760m-plus-wifi2-d5":"充新商品且缺少WIFI II身份，不作为全新该型号报价",
    "mb-asus-tuf-b850m-plus-wifi7":"B850/B650、重炮手/吹雪混列，需精确选中型号",
    "mb-asus-prime-h610m-a":"充新商品且多个主板型号混列",
    "mb-gb-b760m-aorus-elite-d4":"候选名称WIFI6E与选择行GEN5 DDR4需重新消歧",
    "mb-gb-b760m-aorus-elite-d5":"候选名称GEN5在所选型号尾部未得到确认",
    "mb-gb-x870-aorus-elite":"选中猎鹰，不是小雕AORUS ELITE",
    "mb-msi-b850m-mortar-wifi":"选中B850M GAMING WIFI，不是MORTAR",
    "mb-msi-b850m-edge-ti":"选中B850M-A WIFI，不是EDGE TI",
    "mb-msi-b760m-mortar-wifi2-d5":"选中PRO B760M-B DDR5，不是MORTAR WIFI II",
    "mb-msi-b550m-mortar-wifi":"多个主板系列混列，包装字样不能证明具体变体",
    "mb-msi-x870e-tomahawk":"报价包含MAX WIFI7 PZ修订，不能沿用通用TOMAHAWK身份",
    "mem-klevv-urbane-ddr5-32-6000":"名称C30与报价行C28冲突，需按时序拆分报价",
    "mem-gloway-tiance-ddr5-32-6000":"所选尾部为5600/24GB，与目标6000/32GB不一致",
    "ssd-zhitai-tiplus5000-1tb":"零通电描述不能证明全新，需复核成色",
    "ssd-zhitai-tiplus7100-1tb":"所选型号为TiPlus7100s，与目标TiPlus7100不同",
    "ssd-wd-sn7100-1tb":"金百达/WD/宏碁/梵想混列，无明确选中型号",
    "ssd-crucial-p310-1tb":"选中E100，不是P310",
    "psu-huntkey-wd650evo":"650/750/850W混列，认证也存在跨行冲突",
    "psu-sama-gt850":"500/650/750/850/1000W混列，500W字段不是850W的事实",
    "psu-superflower-zillion2-650":"650/750W混列，需要选中功率的证据",
    "case-sama-pingtouge-m9":"所选为M9 LITE，需以具体LITE变体建档",
    "case-asus-tuf-gt301":"所选为祢豆子联名版，不能混用普通版报价",
    "cooler-thermalright-warframe-240":"所选为240-X白色无风扇版，与通用型号不同",
    "cooler-noctua-nh-l9i":"1700与115x安装版本混列，需要具体型号",
}


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write_json(path, value):
    path.write_bytes((json.dumps(value, ensure_ascii=False, indent=2)+"\n").encode("utf-8"))


def provisional_specs(category, claims):
    result = {key:None for key in SPEC_FIELDS[category]}
    aliases = {"memory":{"ddr":"generation"}, "motherboard":{"ddr":"memory_generation"},
               "psu":{"watt":"wattage_w"}, "cooler":{"radiator_mm":"radiator_size_mm"}}
    for key, value in claims.items():
        target = aliases.get(category, {}).get(key, key)
        if target in result:
            result[target] = {"风冷":"air", "一体式水冷":"aio"}.get(value, value) if isinstance(value,str) else value
    validate_specs(category, result)
    return result


def audit(candidates, offers):
    if len({c["model_id"] for c in candidates}) != len(candidates):
        raise ValueError("duplicate model identity")
    rows = []
    for c in candidates:
        mid = c["model_id"]
        selected = [o for o in offers if o["model_id"] == mid and o["is_primary"]]
        if len(selected) != 1:
            raise ValueError(f"{mid}: expected one original primary offer")
        primary = selected[0]
        if Decimal(str(primary["price_cny"])) != Decimal(str(c["price_min"])):
            raise ValueError(f"{mid}: primary price drift")
        claims = dict(c["specs"])
        correction = CORRECTIONS.get(mid)
        if correction:
            quote, patch = correction
            if quote not in primary["title"]:
                raise ValueError(f"{mid}: reviewed quote drift")
            claims.update(patch)
        proposed = provisional_specs(c["category"], claims)
        required = _required_fields({"category":c["category"], "specs":proposed})
        missing = [f for f in required if proposed[f] is None]
        issues = []
        if mid in IDENTITY_REVIEW:
            issues.append(IDENTITY_REVIEW[mid])
        if correction:
            issues.append("原采集字段与选中报价行不一致，修订候选声明；不因此升级为官网核验事实")
        rows.append({"model_id":mid,"name":c["name"],"category":c["category"],
                     "source_candidate":c,"primary_offer":primary,
                     "title_claims":claims,"proposed_specs":proposed,
                     "missing_compatibility_fields":missing,
                     "missing_official_evidence_fields":list(required),
                     "identity_status":"conflict" if mid in IDENTITY_REVIEW else "needs_exact_variant_review",
                     "issues":issues,"correction_quote":correction[0] if correction else None,
                     "publish_status":"candidate_only"})
    return rows


def freeze_database(out, container):
    # One read-only transaction preserves a consistent parts/evidence/price view.
    query = """BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
    SELECT json_build_object(
      'parts',(SELECT json_agg(p ORDER BY sku) FROM parts p),
      'evidence',(SELECT json_agg(e ORDER BY evidence_id) FROM part_evidence e),
      'snapshots',(SELECT json_agg(s ORDER BY snapshot_date,id) FROM price_snapshots s),
      'prices',(SELECT json_agg(p ORDER BY snapshot_id,sku) FROM prices p));
    COMMIT;"""
    raw = subprocess.check_output(["docker","exec", "-i",container,"psql","-X","-qAt",
                                   "-v","ON_ERROR_STOP=1","-U","pcbuilder","-d","pcbuilder"],input=query.encode())
    data = json.loads(raw)
    write_json(out / "database-snapshot.json", data)
    latest = max(data["snapshots"],key=lambda s:(s["snapshot_date"],s["id"]))
    if latest.get("release_id"):
        manifest=json.loads((ROOT/"var/data/price-releases"/latest["release_id"]/"manifest.json").read_text())
        if manifest["input_sha256"]!=latest["file_sha256"] or manifest["manifest_sha256"]!=latest["manifest_sha256"]:
            raise ValueError("latest database/release mismatch")
    elif digest(ROOT / f"scripts/data/prices/{latest['snapshot_date']}.csv") != latest["file_sha256"]:
        raise ValueError("latest database/file snapshot mismatch")
    latest_prices = {p["sku"]:p for p in data["prices"] if p["snapshot_id"] == latest["id"]}
    summary = {}
    for category in SPEC_FIELDS:
        parts = [p for p in data["parts"] if p["category"] == category and p["active"] and p["catalog_state"]=="active_core"]
        summary[category] = {"active":len(parts),"priced":sum(p["sku"] in latest_prices for p in parts)}
    return {"latest_date":latest["snapshot_date"],"latest_id":latest["id"],"categories":summary,
            "sha256":digest(out / "database-snapshot.json")}


def read_rows(name):
    return [json.loads(line) for line in (SOURCE / name).read_text(encoding="utf-8").splitlines() if line]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--out", required=True, type=Path)
    parser.add_argument("--freeze-db", action="store_true")
    parser.add_argument("--container",default="pc-builder-agent-postgres-1")
    args = parser.parse_args()
    args.out.mkdir(parents=True, exist_ok=False)
    candidates = read_rows("candidates.jsonl")
    offers = read_rows("offers.jsonl")
    rows = audit(candidates, offers)
    write_json(args.out / "candidate-audit.json",rows)
    manifest = {"schema_version":1,"source_type":"aggregator_secondary",
                "review_basis":"Agent辅助审查；候选非正式发布",
                "source_hashes":{name:digest(SOURCE/name) for name in ["candidates.jsonl","offers.jsonl","review-decisions.jsonl"]},
                "script_sha256":digest(Path(__file__)),"candidates":len(rows),
                "identity_conflicts":sum(r["identity_status"]=="conflict" for r in rows),
                "corrected_claims":sum(r["correction_quote"] is not None for r in rows),
                "collection_claims_complete":sum(not c["missing_fields"] for c in candidates),
                "compatibility_claims_complete":sum(not r["missing_compatibility_fields"] for r in rows),
                "official_evidence_complete":0,"new_model_api_calls":0,"new_search_calls":0}
    if args.freeze_db:
        manifest["database"] = freeze_database(args.out,args.container)
    write_json(args.out / "manifest.json",manifest)
    lines = ["# 新型号接入审查", "", "本报告不发布商品、不改写原始采集。字段映射只是候选声明，尚无官方证据。", "",
             "| 型号 | 身份问题/修订 | 缺失兼容字段 |", "| --- | --- | --- |"]
    for r in rows:
        lines.append("| "+r["model_id"]+" | "+"；".join(r["issues"] or ["待精确变体复核"])+" | "+", ".join(r["missing_compatibility_fields"])+" |")
    (args.out / "README.md").write_bytes(("\n".join(lines)+"\n").encode("utf-8"))
    print(json.dumps(manifest,ensure_ascii=False,indent=2))


if __name__ == "__main__":
    main()
