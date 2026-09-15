"""Archive only the successfully published additions; keep original collection intact."""
import json
import argparse
from pathlib import Path
from audit_candidates import ROOT, write_json, digest
from prepare_amd_intake import jsonl


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument("--packet",type=Path,default=ROOT/"artifacts/data-intake-amd-20260914-r2")
    parser.add_argument("--publication",type=Path,default=ROOT/"artifacts/data-intake-publication-20260914-r1/result.json")
    parser.add_argument("--out",type=Path,default=ROOT/"scripts/data/reviewed-intake/2026-09-14-amd")
    args=parser.parse_args()
    packet,publication=args.packet,args.publication
    result=json.loads(publication.read_text())
    if result["mode"]!="published": raise ValueError("publication required")
    selected=json.loads((packet/"selected-offers.json").read_text(encoding="utf-8"))
    ids={i["sku"] for i in selected}
    rows=[json.loads(line) for path in (packet/"parts").glob("*.jsonl") for line in path.read_text(encoding="utf-8").splitlines()]
    additions=[r for r in rows if r["sku"] in ids]
    evidence=[json.loads(line) for line in (packet/"evidence/fields.jsonl").read_text(encoding="utf-8").splitlines() if json.loads(line)["sku"] in ids]
    originals={}
    for category in {r["category"] for r in additions}:
        seed=ROOT/"scripts/data/parts"/(category+".jsonl")
        original=seed.read_bytes()
        if any(json.loads(line)["sku"] in ids for line in original.decode("utf-8").splitlines()):
            raise ValueError("seed already contains additions; do not append again")
        originals[category]=(seed,original)
    archive=args.out
    archive.mkdir(parents=True,exist_ok=False)
    jsonl(archive/"parts.jsonl",additions)
    jsonl(archive/"evidence.jsonl",evidence)
    write_json(archive/"offers.json",selected)
    write_json(archive/"publication.json",result)
    write_json(archive/"manifest.json",{"source_type":"aggregator_secondary","review_basis":"Agent辅助逐行复核，手动发布",
        "official_evidence_fields":len(evidence),"models":len(additions),
        "files":{name:digest(archive/name) for name in ["parts.jsonl","evidence.jsonl","offers.json","publication.json"]},
        "raw_html_directory":json.loads((packet/"packet.json").read_text()).get("official_pages","artifacts/data-intake-official-20260914-r1 and r2"),
        "price_observed_dates":sorted({item["observed_date"] for item in selected}),"price_precision":"day","inventory":"unknown"})
    # Append only: preserve original seed bytes and historical item serialization.
    for category,(seed,original) in originals.items():
        extra="\n".join(json.dumps(r,ensure_ascii=False,sort_keys=True) for r in additions if r["category"]==category)+"\n"
        seed.write_bytes(original+(b"" if original.endswith(b"\n") else b"\n")+extra.encode("utf-8"))
    print(f"Archived {len(additions)} published additions, {len(evidence)} evidence fields; original seed bytes retained")


if __name__=="__main__":main()
