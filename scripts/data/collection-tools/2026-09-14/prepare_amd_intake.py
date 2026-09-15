"""Prepare a reviewable AMD intake packet using saved official pages and offers.

No database writes, price date rewriting or publishing. Existing releases remain
byte-for-byte inputs. The normal release publisher consumes the prepared files.
"""
import argparse
import json
import uuid
from pathlib import Path
from audit_candidates import ROOT, read_rows, digest, write_json
from pcdata.amd import parse_amd_cpu_html, build_amd_evidence
from pcdata.automation import create_review
from pcdata.canonical import validate_part

PRODUCTS = {
    "amd-4500":("cpu-r5-4500","cpu-r5-4500","5","4500","散片"),
    "amd-5700x3d":("cpu-r5-5700x3d","cpu-r7-5700x3d","7","5700X3D","散片"),
    "amd-5800x3d":("cpu-r7-5800x3d","cpu-r7-5800x3d","7","5800X3D","盒装"),
    "amd-8400f":("cpu-r5-8400f","cpu-r5-8400f","5","8400F","散片"),
    "amd-8700f":("cpu-r7-8700f","cpu-r7-8700f","7","8700F","散片"),
    "amd-8700g":("cpu-r7-8700g","cpu-r7-8700g","7","8700G","散片"),
    "amd-7900x":("cpu-r9-7900x","cpu-r9-7900x","9","7900X","盒装"),
    "amd-9950x3d":("cpu-r9-9950x3d","cpu-r9-9950x3d","9","9950X3D","散片"),
}


def jsonl(path,rows):
    path.parent.mkdir(parents=True,exist_ok=True)
    path.write_bytes(("\n".join(json.dumps(r,ensure_ascii=False,sort_keys=True) for r in rows)+"\n").encode())


def prepare(out,page_dirs):
    if digest(ROOT/"scripts/data/catalog-candidates/2026-09-13/offers.jsonl") != "1152debccc4a319932d9a9971493531bb5d5b89b9f9e895fb028f4160567b0b6":
        raise ValueError("reviewed offers changed; review the new batch before preparing")
    current=json.loads((ROOT/"var/data/current.json").read_text())
    base=ROOT/"var/data/releases"/current["release_id"]
    pages={}
    for folder in page_dirs:
        for record in json.loads((folder/"manifest.json").read_text())["pages"]:
            if record["status"]=="parsed_needs_publication_review" and not record.get("error"):
                if record["id"] in pages: raise ValueError("ambiguous official capture")
                pages[record["id"]]=(folder,record)
    offers=read_rows("offers.jsonl")
    primary={o["model_id"]:o for o in offers if o["is_primary"]}
    base_rows={}
    for path in (base/"parts").glob("*.jsonl"):
        base_rows[path.name]=[json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line]
    all_ids={r["sku"] for rows in base_rows.values() for r in rows}
    evidence=[json.loads(line) for line in (base/"evidence/fields.jsonl").read_text().splitlines() if line]
    selected=[]
    for key,(origin,sku,tier,number,packaging) in PRODUCTS.items():
        folder,capture=pages[key]
        raw_path=folder/(key+".html")
        if digest(raw_path)!=capture["raw_sha256"]: raise ValueError("official capture drift")
        parsed,quotes=parse_amd_cpu_html(raw_path.read_bytes(),f"AMD Ryzen {tier} {number}")
        offer=primary[origin]
        # Review identified the selected variant at the tail, not the multi-SKU prefix.
        tail=offer["title"][-45:]
        if number not in tail.upper() or packaging not in tail.replace("原盒","盒装"):
            raise ValueError(f"selected offer identity/packaging drift: {origin}")
        if sku in all_ids: raise ValueError(f"SKU already exists: {sku}")
        row={"sku":sku,"category":"cpu","brand":"AMD","model":f"Ryzen {tier} {number}","schema_version":1,
             "catalog_state":"active_core","specs":{k.removeprefix("specs."):v for k,v in parsed.items() if k.startswith("specs.")},
             "source_meta":{"intake":{"origin_model_id":origin,"official_url":capture["url"],"raw_sha256":capture["raw_sha256"],
                                        "captured_at":capture["captured_at"],"review_basis":"Agent辅助型号及报价行复核", "packaging":packaging,
                                        "offer_row_key":offer["row_key"],"source_type":"aggregator_secondary"}}}
        validate_part(row)
        new_evidence=build_amd_evidence(source_id="amd_products",source_url=capture["url"],sku=sku,
                                      expected_name=f"AMD Ryzen {tier} {number}",current=row,parsed=parsed,excerpts=quotes,
                                      raw_sha256=capture["raw_sha256"],captured_at=capture["captured_at"])
        if any(e["evidence_status"]!="verified" for e in new_evidence): raise ValueError("official mismatch")
        base_rows["cpu.jsonl"].append(row)
        evidence.extend(new_evidence)
        selected.append({"sku":sku,"original_model_id":origin,"packaging":packaging,"offer":offer,
                         "observed_date":"2026-09-13","source_type":"aggregator_secondary","stock_status":"unknown",
                         "raw_source_file":"scripts/data/catalog-candidates/2026-09-13/offers.jsonl",
                         "raw_source_sha256":digest(ROOT/"scripts/data/catalog-candidates/2026-09-13/offers.jsonl")})
    for name,rows in base_rows.items(): jsonl(out/"parts"/name,rows)
    jsonl(out/"evidence/fields.jsonl",evidence)
    write_json(out/"selected-offers.json",selected)
    run_id=str(uuid.uuid5(uuid.NAMESPACE_URL,"pcbuilder-intake:"+digest(out/"selected-offers.json")+digest(out/"evidence/fields.jsonl")))
    review=create_review(run_id=run_id,candidate_parts=out/"parts",base_parts=base/"parts",
                         base_release_id=current["release_id"],candidate_evidence=out/"evidence/fields.jsonl",
                         base_evidence=base/"evidence/fields.jsonl",model_used=True)
    if any(c["kind"]!="added" for c in review["changes"]): raise ValueError("unexpected existing part modification")
    write_json(out/"review.json",review)
    write_json(out/"packet.json",{"status":"prepared_not_published","added":len(selected),"official_evidence_added":len(selected)*5,
                                 "base_release":current["release_id"],"price_observed_date_preserved":True,
                                 "selected_offers_sha256":digest(out/"selected-offers.json"),"input_sha256":review["input_sha256"]})
    return selected


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument("--out",required=True,type=Path)
    parser.add_argument("--pages",required=True,nargs="+",type=Path)
    args=parser.parse_args()
    args.out.mkdir(parents=True,exist_ok=False)
    selected=prepare(args.out,args.pages)
    print(f"Prepared {len(selected)} AMD products; no publication or database writes")


if __name__=="__main__":main()
