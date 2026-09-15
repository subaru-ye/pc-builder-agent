"""Compare frozen price batches with identical parts; no model or database writes."""
import argparse
import hashlib
import json
from collections import Counter
from decimal import Decimal
from pathlib import Path

ROOT=Path(__file__).resolve().parents[3]


def compare(data,old_date,new_date,old_id=None,new_id=None):
    def resolve(day,identity):
        matches=[s for s in data["snapshots"] if s["snapshot_date"]==day and (identity is None or s["id"]==identity)]
        if len(matches)!=1:
            raise ValueError(f"Snapshot {day} is missing or ambiguous; specify its ID")
        return matches[0]["id"]
    old_id,new_id=resolve(old_date,old_id),resolve(new_date,new_id)
    old={p["sku"]:p for p in data["prices"] if p["snapshot_id"]==old_id}
    new={p["sku"]:p for p in data["prices"] if p["snapshot_id"]==new_id}
    active=[p for p in data["parts"] if p["active"] and p["catalog_state"]=="active_core"]
    categories={}
    for category in sorted({p["category"] for p in active}):
        ids={p["sku"] for p in active if p["category"]==category}
        categories[category]={"catalog":len(ids),"old_priced":len(ids&old.keys()),"new_priced":len(ids&new.keys()),
                              "removed_price_skus":sorted(ids&old.keys()-new.keys())}
    changed=[{"sku":sku,"old":old[sku]["price_cny"],"new":new[sku]["price_cny"],
              "old_observed_at":old[sku]["observed_at"],"new_observed_at":new[sku]["observed_at"]}
             for sku in sorted(old.keys()&new.keys()) if Decimal(str(old[sku]["price_cny"]))!=Decimal(str(new[sku]["price_cny"]))]
    result={"old_date":old_date,"new_date":new_date,"old_id":old_id,"new_id":new_id,"categories":categories,"changed_prices":changed,
            "old_observed_dates":dict(Counter(p["observed_at"][:10] for p in old.values())),
            "new_observed_dates":dict(Counter(p["observed_at"][:10] for p in new.values())),
            "limitations":["Structural price comparison, not live model quality or completed-build success rate.",
                            "Identical SKU does not prove identical packaging; original offer audit remains necessary.",
                            "No fallback to stale prices. Missing selection quote is incomplete, not a zero-cost part."]}
    recording=json.loads((ROOT/"internal/planning/testdata/complete_proposal_recording.json").read_text(encoding="utf-8"))["result"]
    # Reuse persisted quote quantities, including multiple identical SSDs.
    lines=recording["quote"]["lines"]
    result["saved_build_fixed_candidates"]={}
    for name,prices in [("old",old),("new",new)]:
        result["saved_build_fixed_candidates"][name]=fixed_quote(prices,lines)
    return result


def fixed_quote(prices,lines):
    missing=sorted({line["sku"] for line in lines if line["sku"] not in prices})
    subtotal=Decimal(0)
    for line in lines:
        quantity=line["quantity"]
        if type(quantity) is not int or quantity<=0:
            raise ValueError("invalid persisted quote quantity")
        if line["sku"] in prices:
            subtotal+=Decimal(str(prices[line["sku"]]["price_cny"]))*quantity
    return {"missing":missing,"known_subtotal":str(subtotal),"complete_total":None if missing else str(subtotal)}


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument("--snapshot",required=True,type=Path)
    parser.add_argument("--old",default="2026-09-08")
    parser.add_argument("--new",default="2026-09-14")
    parser.add_argument("--old-id",type=int)
    parser.add_argument("--new-id",type=int)
    parser.add_argument("--out",required=True,type=Path)
    args=parser.parse_args()
    raw=args.snapshot.read_bytes()
    result=compare(json.loads(raw),args.old,args.new,args.old_id,args.new_id)
    result["input_sha256"]=hashlib.sha256(raw).hexdigest()
    with args.out.open("xb") as f:
        f.write((json.dumps(result,ensure_ascii=False,indent=2)+"\n").encode("utf-8"))
    print(json.dumps(result,ensure_ascii=False,indent=2))


if __name__=="__main__":main()
