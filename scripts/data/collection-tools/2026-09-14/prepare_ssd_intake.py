"""Prepare three explicitly reviewed SSD variants from saved official pages."""
import argparse
import hashlib
import json
import uuid
from pathlib import Path
from audit_candidates import ROOT, digest, write_json
from fetch_intake_specs import decode_page
from prepare_amd_intake import jsonl
from pcdata.automation import _VisiblePolicyTextParser, create_review
from pcdata.canonical import validate_part

CHOICES = [
    ("ssd-zhitai-ti600-1tb","zhitai-ti600","ZHITAI","Ti600 1TB","8ff6e159e9c63838","致态Ti600固态硬盘1TB SSD"),
    ("ssd-zhitai-ti600-2tb","zhitai-ti600","ZHITAI","Ti600 2TB","e6c460848a24ad71","Ti600 Gen4 发轫之作 【2TB】"),
    ("ssd-wd-sn7100-500g","wd-sn7100","WD","WD_BLACK SN7100 500GB","352d20fac7ebec3a","SN7100  500G【全新旗舰款】"),
]


def check_spec(key, text):
    if key == "zhitai-ti600":
        required=["ZHITAI Ti600 SSD Specification Sheet*","Form Factor\nM.2 2280","Capacity\n500GB\n1TB\n2TB\n4TB"]
    else:
        required=["500GB WD_BLACK SN7100 NVMe SSD", "Capacity\n500GB\nForm Factor\nM.2 2280", "500GB:\nWDS500G4X0E-00CJA0"]
    if any(value not in text for value in required):
        raise ValueError(f"official identity/capacity/specification block changed: {key}")
    return required,{"form_factor":("m2",required[1])}


def prepare(out, pages, choices=CHOICES, check=check_spec, category="ssd", offer_source=None, offer_sha256=None):
    offer_source=offer_source or ROOT/"scripts/data/catalog-candidates/2026-09-13/offers.jsonl"
    expected_hash=offer_sha256 or "1152debccc4a319932d9a9971493531bb5d5b89b9f9e895fb028f4160567b0b6"
    if digest(offer_source) != expected_hash:
        raise ValueError("reviewed original offers changed")
    current=json.loads((ROOT/"var/data/current.json").read_text())
    base=ROOT/"var/data/releases"/current["release_id"]
    captures={r["id"]:r for r in json.loads((pages/"manifest.json").read_text())["pages"]}
    offer_rows=[json.loads(line) for line in offer_source.read_text(encoding="utf-8").splitlines() if line]
    offers={o["row_key"]:o for o in offer_rows}
    if len(offers)!=len(offer_rows): raise ValueError("duplicate reviewed offer row")
    rows={p.name:[json.loads(line) for line in p.read_text(encoding="utf-8").splitlines() if line] for p in (base/"parts").glob("*.jsonl")}
    existing={r["sku"] for items in rows.values() for r in items}
    evidence=[json.loads(line) for line in (base/"evidence/fields.jsonl").read_text(encoding="utf-8").splitlines() if line]
    selected=[]
    added_evidence=0
    for sku,key,brand,model,row_key,needle in choices:
        if sku in existing: raise ValueError(f"SKU already exists: {sku}")
        capture=captures[key]
        raw=pages/(key+".html")
        if capture.get("http_status")!=200 or digest(raw)!=capture["raw_sha256"]:
            raise ValueError("capture changed or not successful")
        reader=_VisiblePolicyTextParser()
        reader.feed(decode_page(raw.read_bytes()))
        text="\n".join(reader.parts)
        required,fields=check(key,text)
        offer=offers[row_key]
        if offer["model_id"]!=sku or needle not in offer["title"] or not offer["buy_url"]:
            raise ValueError("reviewed offer variant drift")
        part_category=category or offer["category"]
        row={"sku":sku,"category":part_category,"brand":brand,"model":model,"schema_version":1,
             "catalog_state":"active_core","specs":{field:value[0] for field,value in fields.items()},
             "source_meta":{"intake":{"origin_model_id":sku,"official_url":capture["url"],"raw_sha256":capture["raw_sha256"],
                 "captured_at":capture["captured_at"],"review_basis":"Agent辅助精确型号、报价行与官方规格复核",
                 "offer_row_key":row_key,"source_type":"aggregator_secondary","packaging":"unknown"}}}
        validate_part(row)
        for field,value,quote in [("model",model,"; ".join(required))]+[("specs."+field,value[0],value[1]) for field,value in fields.items()]:
            identity=dict(source_id=key+"_manual_official",sku=sku,field=field,value=value,raw_sha256=capture["raw_sha256"])
            eid=hashlib.sha256(json.dumps(identity,ensure_ascii=False,sort_keys=True,separators=(",",":")).encode()).hexdigest()
            evidence.append(dict(schema_version=1,id=eid,**identity,source_url=capture["url"],captured_at=capture["captured_at"],
                                 method="deterministic",evidence_excerpt=quote,evidence_status="verified"))
            added_evidence+=1
        rows[part_category+".jsonl"].append(row)
        selected.append(dict(sku=sku,original_model_id=sku,packaging="unknown",offer=offer,observed_date=offer.get("detail_observed_date","2026-09-13"),
                             source_type="aggregator_secondary",stock_status="unknown",
                             raw_source_file=offer_source.resolve().relative_to(ROOT).as_posix(),
                             raw_source_sha256=digest(offer_source)))
    for name,items in rows.items(): jsonl(out/"parts"/name,items)
    jsonl(out/"evidence/fields.jsonl",evidence)
    write_json(out/"selected-offers.json",selected)
    run_id=str(uuid.uuid5(uuid.NAMESPACE_URL,"pcbuilder-intake:"+digest(out/"selected-offers.json")+digest(out/"evidence/fields.jsonl")))
    review=create_review(run_id=run_id,candidate_parts=out/"parts",base_parts=base/"parts",base_release_id=current["release_id"],
                         candidate_evidence=out/"evidence/fields.jsonl",base_evidence=base/"evidence/fields.jsonl",model_used=True)
    if len(review["changes"])!=len(choices) or any(c["kind"]!="added" for c in review["changes"]): raise ValueError("unexpected diff")
    write_json(out/"review.json",review)
    price_pointer=json.loads((ROOT/"var/data/current-price.json").read_text())
    price_manifest=json.loads((ROOT/"var/data/price-releases"/price_pointer["release_id"]/"manifest.json").read_text())
    write_json(out/"packet.json",dict(status="prepared_not_published",added=len(choices),official_evidence_added=added_evidence,base_release=current["release_id"],
        base_parts=len(existing),base_priced=price_manifest["stats"]["selected"],base_price_release=price_pointer["release_id"],official_pages=str(pages),
        base_price_hash=price_manifest["input_sha256"],selected_offers_sha256=digest(out/"selected-offers.json"),input_sha256=review["input_sha256"]))
    print(f"Prepared {len(choices)} {category or 'mixed-category'} variants, {added_evidence} reviewed official fields; no database writes")


if __name__=="__main__":
    parser=argparse.ArgumentParser()
    parser.add_argument("--out",required=True,type=Path)
    parser.add_argument("--pages",required=True,type=Path)
    args=parser.parse_args()
    args.out.mkdir(parents=True,exist_ok=False)
    prepare(args.out,args.pages)
