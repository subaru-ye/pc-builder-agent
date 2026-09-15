"""Publish a reviewed intake through P11/P12; rehearse in an isolated DB first.

No network collection or model calls. Original offers are date-precision
observations; midnight UTC is the existing loader's date-only representation.
"""
import argparse
import json
import os
import shutil
import subprocess
from decimal import Decimal
from pathlib import Path

from audit_candidates import ROOT, digest, write_json
from pcdata.automation import DataPaths, publish_reviewed_release
from pcdata.prices import _observation_identity, import_price_observations, create_price_review, publish_price_review


def run(*args):
    result = subprocess.run(list(args), cwd=ROOT, capture_output=True, encoding="utf-8")
    if result.returncode:
        # These importers never print DSNs; retain their actionable validation error.
        raise RuntimeError(result.stderr)
    return result.stdout


def observe(selected):
    rows=[]
    for item in selected:
        offer=item["offer"]
        if digest(ROOT/item["raw_source_file"])!=item["raw_source_sha256"]:
            raise ValueError("reviewed raw offers changed")
        row=dict(schema_version=1,sku=item["sku"],price_cny=f"{Decimal(str(offer['price_cny'])):.2f}",
                 currency="CNY",source_id=f"maishou88:{offer['platform_src']}:{offer['shop']}",
                 collector_id="maishou_reviewed",product_id=offer["goodsId"],source_url=offer["buy_url"],
                 seller=offer["shop"],price_type="listing",availability_basis="unknown",stock_status="unknown",
                 variant_match="exact",observed_at=item["observed_date"]+"T00:00:00Z",
                 raw_sha256=item["raw_source_sha256"],decision_status="qualified",rejection_reasons=[])
        row["observation_id"]=_observation_identity(row)
        rows.append(row)
    return rows


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument("--packet",required=True,type=Path)
    parser.add_argument("--out",required=True,type=Path)
    parser.add_argument("--container",required=True)
    parser.add_argument("--database",required=True)
    parser.add_argument("--db-user",required=True)
    mode=parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--rehearsal",action="store_true")
    mode.add_argument("--publish",action="store_true")
    args=parser.parse_args()
    args.out.mkdir(parents=True,exist_ok=False)
    packet=args.packet.resolve()
    meta=json.loads((packet/"packet.json").read_text())
    expected_parts=meta.get("base_parts",160)+meta["added"]
    expected_priced=meta.get("base_priced",108)+meta["added"]
    selected=json.loads((packet/"selected-offers.json").read_text(encoding="utf-8"))
    if digest(packet/"selected-offers.json")!=meta["selected_offers_sha256"]:
        raise ValueError("selected offer review drift")
    observations=observe(selected)
    review=json.loads((packet/"review.json").read_text(encoding="utf-8"))
    paths=DataPaths.defaults()
    base=paths.releases/meta["base_release"]

    def query(sql):
        return json.loads(run("docker","exec",args.container,"psql","-X","-qAt","-v","ON_ERROR_STOP=1",
                              "-U",args.db_user,"-d",args.database,"-c",sql))

    def frozen():
        return query("SELECT json_build_object('parts',(SELECT json_agg(p ORDER BY sku) FROM parts p),"
                     "'snapshots',(SELECT json_agg(s ORDER BY snapshot_date,id) FROM price_snapshots s),"
                     "'prices',(SELECT json_agg(p ORDER BY snapshot_id,sku) FROM prices p),"
                     "'history_md5',(SELECT md5(COALESCE(json_agg(b ORDER BY id)::text,'')) FROM builds b),"
                     "'requirements_md5',(SELECT md5(COALESCE(json_agg(r ORDER BY id)::text,'')) FROM requirements r))")

    if args.rehearsal:
        if not args.container.startswith("pcbuilder-planning-eval-") or not os.environ.get("PG_DSN"):
            raise ValueError("rehearsal requires the isolated evaluation launcher")
        paths=DataPaths(ROOT,args.out.resolve()/"data",args.out.resolve()/"runtime")
        shutil.copytree(ROOT/"scripts/data/prices",paths.seed_prices)
        paths.ensure()
        shutil.copytree(base,paths.releases/base.name)
        shutil.copyfile(ROOT/"var/data/current.json",paths.current)
        run("go","run","./cmd/migrate","up")
        ancestry=[]
        ancestor=base
        while ancestor is not None:
            ancestry.append(ancestor)
            previous=json.loads((ancestor/"manifest.json").read_text()).get("previous_release_id")
            ancestor=ROOT/"var/data/releases"/previous if previous else None
        for release in reversed(ancestry):
            run("go","run","./cmd/importparts","-dir",str(release/"parts"),"-release-manifest",str(release/"manifest.json"))
        for day in ("2026-07-28","2026-09-08","2026-09-13","2026-09-14"):
            run("go","run","./cmd/importprices","-file",str(paths.seed_prices/(day+".csv")),"-snapshot-date",day)
        price_release=meta.get("base_price_release")
        price_ancestry=[]
        while price_release:
            release=ROOT/"var/data/price-releases"/price_release
            price_ancestry.append(release)
            price_release=json.loads((release/"manifest.json").read_text()).get("previous_release_id")
        for release in reversed(price_ancestry):
            run("go","run","./cmd/importpriceobservations","-release",str(release))
            shutil.copytree(release,paths.runtime_root/"price-releases"/release.name)
        if price_ancestry:
            shutil.copyfile(ROOT/"var/data/current-price.json",paths.runtime_root/"current-price.json")
    elif (args.container,args.database,args.db_user)!=("pc-builder-agent-postgres-1","pcbuilder","pcbuilder"):
        raise ValueError("unexpected publication target")

    before=frozen()
    write_json(args.out/"before.json",before)
    latest=max(before["snapshots"],key=lambda s:(s["snapshot_date"],s["id"]))
    if latest["file_sha256"]!=meta.get("base_price_hash","63189ab03b7dc4e0264a782e033a001c5e8fbc50e09e6239273696292d820744"):
        raise ValueError("price baseline changed; rebase review before publication")
    if len(before["parts"])!=meta.get("base_parts",160):
        raise ValueError("part baseline changed; rebase review before publication")
    if not args.rehearsal:
        run("go","run","./cmd/migrate","up")
    result=publish_reviewed_release(paths,run_id=review["run_id"],candidate_parts=packet/"parts",
                                    candidate_evidence=packet/"evidence/fields.jsonl",review=review,policy="manual")
    write_json(args.out/"parts-publication.json",result)
    imported=import_price_observations(paths,observations,run_id=review["run_id"],
                                       source_file_sha256=selected[0]["raw_source_sha256"])
    prices=create_price_review(paths,imported["run_id"])
    if prices["quarantined"] or set(prices["changed_skus"])!={i["sku"] for i in selected}:
        raise ValueError("unexpected price review; parts published, price publication stopped")
    published=publish_price_review(paths,imported["run_id"],policy="manual")
    retry=publish_price_review(paths,imported["run_id"],policy="manual")
    assert retry["release_id"]==published["release_id"]
    after=frozen()
    write_json(args.out/"after.json",after)
    old_parts={p["sku"]:{k:v for k,v in p.items() if k!="updated_at"} for p in before["parts"]}
    new_parts={p["sku"]:{k:v for k,v in p.items() if k!="updated_at"} for p in after["parts"]}
    assert all(new_parts[k]==v for k,v in old_parts.items()), "existing parts modified"
    old_ids={s["id"] for s in before["snapshots"]}
    assert [p for p in after["prices"] if p["snapshot_id"] in old_ids]==before["prices"], "old quotes modified"
    assert after["history_md5"]==before["history_md5"] and after["requirements_md5"]==before["requirements_md5"]
    newest=max(after["snapshots"],key=lambda s:(s["snapshot_date"],s["id"]))
    quotes=[p for p in after["prices"] if p["snapshot_id"]==newest["id"]]
    assert len(after["parts"])==expected_parts and len(quotes)==expected_priced and len(after["snapshots"])==len(old_ids)+1
    observed_dates={i["sku"]:i["observed_date"] for i in selected}
    assert all(p["observed_at"].startswith(observed_dates[p["sku"]]) and p["availability_basis"]=="unknown"
               for p in quotes if p["sku"] in observed_dates)
    write_json(args.out/"result.json",dict(mode="rehearsal" if args.rehearsal else "published",parts=expected_parts,priced=expected_priced,
               parts_release=result["release_id"],price_release=published["release_id"],snapshot_id=newest["id"],
               existing_parts_unchanged=True,history_unchanged=True,price_retry_idempotent=True,
               observation_date_precision="day",model_calls=0,external_search_calls=0))
    print(f"Verified: {expected_parts} parts, {expected_priced} quotes; old parts, quotes and user history preserved; retry idempotent",flush=True)


if __name__=="__main__": main()
