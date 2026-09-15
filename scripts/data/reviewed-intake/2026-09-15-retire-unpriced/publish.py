"""Retire the reviewed 52 unpriced SKUs through the existing release pipeline.

Default: prepare and rehearse against an independent database cloned locally.
--publish: publish the exact rehearsed packet; never collect prices or call models.
"""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import uuid
from urllib.parse import urlsplit, urlunsplit

ROOT = Path(__file__).resolve().parents[4]
sys.path.insert(0, str(ROOT / "scripts/data/src"))
from pcdata.automation import DataPaths, create_review, publish_reviewed_release

CONTAINER = "pc-builder-agent-postgres-1"
DATABASE = "pcbuilder"
BASE = "4a3da72c-e001-57dd-ad26-9838c90ade72"
PACKET = Path(__file__).resolve().parent
OUT = ROOT / "artifacts/catalog-retire-unpriced-20260915"


def run(*command, **kwargs):
    return subprocess.run(command, cwd=ROOT, check=True, capture_output=True, **kwargs).stdout


def query(database, sql):
    return json.loads(run("docker", "exec", CONTAINER, "psql", "-XqAt", "-v", "ON_ERROR_STOP=1",
                          "-U", DATABASE, "-d", database, "-c", sql))


def write(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")


def snapshot(database):
    return query(database, """SELECT json_build_object(
        'parts', (SELECT json_agg(json_build_object('sku',sku,'category',category,'active',active,
            'catalog_state',catalog_state,'facts_md5',md5((to_jsonb(p)-'active'-'catalog_state'-'updated_at')::text)) ORDER BY sku) FROM parts p),
        'snapshot_id', (SELECT id FROM price_snapshots ORDER BY snapshot_date DESC,id DESC LIMIT 1),
        'priced', (SELECT json_agg(sku ORDER BY sku) FROM prices WHERE snapshot_id=(SELECT id FROM price_snapshots ORDER BY snapshot_date DESC,id DESC LIMIT 1)),
        'prices_md5', (SELECT md5(json_agg(p ORDER BY snapshot_id,sku)::text) FROM prices p),
        'snapshots_md5', (SELECT md5(json_agg(p ORDER BY id)::text) FROM price_snapshots p),
        'builds_md5', (SELECT md5(coalesce(json_agg(b ORDER BY id)::text,'')) FROM builds b),
        'requirements_md5', (SELECT md5(coalesce(json_agg(r ORDER BY id)::text,'')) FROM requirements r),
        'proposals_md5', (SELECT md5(coalesce(json_agg(p ORDER BY session_id)::text,'')) FROM session_proposals p),
        'evidence_md5', (SELECT md5(coalesce(json_agg(e ORDER BY evidence_id)::text,'')) FROM part_evidence e))""")


def verify(before, after, removed):
    assert before.keys() == after.keys()
    assert all(before[k] == after[k] for k in before if k != "parts"), "quotes or user history changed"
    expected = [{**p, "active": False, "catalog_state": "retired"} if p["sku"] in removed else p for p in before["parts"]]
    assert after["parts"] == expected, "unexpected part mutation"
    active = {p["sku"] for p in after["parts"] if p["active"] and p["catalog_state"] == "active_core"}
    assert active == set(after["priced"]) and len(active) == 123


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--publish", action="store_true")
    args = parser.parse_args()
    paths = DataPaths.defaults()
    before = snapshot(DATABASE)
    assert before["snapshot_id"] == 9 and len(before["parts"]) == 175 and len(before["priced"]) == 123
    current = json.loads(paths.current.read_text(encoding="utf-8"))
    if args.publish:
        rehearsal = json.loads((OUT / "rehearsal.json").read_text(encoding="utf-8"))
        assert rehearsal["verified"] and rehearsal["base_release"] == BASE
        decisions = json.loads((PACKET / "decisions.json").read_text(encoding="utf-8"))
        removed = set(decisions["removed_skus"])
        assert len(removed) == 52 and not removed.intersection(before["priced"])
        if current["release_id"] == rehearsal["release_id"]:
            assert all(not p["active"] and p["catalog_state"] == "retired" for p in before["parts"] if p["sku"] in removed)
            print("Already published; no changes")
            return
        assert current["release_id"] == BASE
    else:
        assert current["release_id"] == BASE
        OUT.mkdir(parents=True, exist_ok=False)
        removed = {p["sku"] for p in before["parts"] if p["active"] and p["sku"] not in before["priced"]}
        assert len(removed) == 52
        write(PACKET / "decisions.json", dict(base_release=BASE, snapshot_id=9, removed_skus=sorted(removed),
              reason="用户明确要求删除缺价商品；退出当前目录，不删除历史报价、规格证据或已保存配置。"))
        shutil.copytree(paths.releases / BASE / "parts", OUT / "parts")
        shutil.copyfile(paths.releases / BASE / "evidence/fields.jsonl", OUT / "fields.jsonl")
        for path in (OUT / "parts").glob("*.jsonl"):
            lines = []
            for line in path.read_text(encoding="utf-8").splitlines():
                record = json.loads(line)
                if record["sku"] in removed:
                    record["catalog_state"] = "retired"
                    line = json.dumps(record, ensure_ascii=False, sort_keys=True)
                lines.append(line)
            path.write_text("\n".join(lines) + "\n", encoding="utf-8", newline="\n")
        review = create_review(run_id=str(uuid.uuid4()), candidate_parts=OUT / "parts", base_parts=paths.releases / BASE / "parts",
                               base_release_id=BASE, candidate_evidence=OUT / "fields.jsonl", base_evidence=paths.releases / BASE / "evidence/fields.jsonl")
        assert {c["sku"] for c in review["changes"]} == removed
        write(OUT / "review.json", review)
    review = json.loads((OUT / "review.json").read_text(encoding="utf-8"))
    assert {c["sku"] for c in review["changes"]} == removed
    write(OUT / ("production-before.json" if args.publish else "before.json"), before)
    database = DATABASE if args.publish else "retire_eval_" + uuid.uuid4().hex[:12]
    if not args.publish:
        run("docker", "exec", CONTAINER, "createdb", "-U", DATABASE, database)
    original_dsn = os.environ.get("PG_DSN")
    try:
        if not args.publish:
            dump = run("docker", "exec", CONTAINER, "pg_dump", "-U", DATABASE, "-Fc", DATABASE)
            run("docker", "exec", "-i", CONTAINER, "pg_restore", "--exit-on-error", "-U", DATABASE, "-d", database, input=dump)
            env = dict(line.split("=", 1) for line in (ROOT / ".env").read_text(encoding="utf-8-sig").splitlines() if line.startswith("PG_DSN="))
            dsn = urlsplit((original_dsn or env["PG_DSN"]).strip().strip("\"'"))
            os.environ["PG_DSN"] = urlunsplit(dsn._replace(path="/" + database))
            isolated = DataPaths(ROOT, OUT / "data", OUT / "runtime")
            isolated.ensure()
            shutil.copytree(paths.releases / BASE, isolated.releases / BASE)
            shutil.copyfile(paths.current, isolated.current)
            paths = isolated
            assert snapshot(database) == before
        release = publish_reviewed_release(paths, run_id=review["run_id"], candidate_parts=OUT / "parts",
                                           candidate_evidence=OUT / "fields.jsonl", review=review, policy="manual")
        after = snapshot(database)
        verify(before, after, removed)
        write(OUT / ("production-after.json" if args.publish else "after.json"), after)
        result = dict(verified=True, base_release=BASE, release_id=release["release_id"], retired=52, active=123,
                      priced=123, history_unchanged=True, model_requests=0, external_requests=0)
        write(OUT / ("publication.json" if args.publish else "rehearsal.json"), result)
        print(json.dumps(result))
    finally:
        if original_dsn is None:
            os.environ.pop("PG_DSN", None)
        else:
            os.environ["PG_DSN"] = original_dsn
        if not args.publish:
            run("docker", "exec", CONTAINER, "dropdb", "-U", DATABASE, database)


if __name__ == "__main__":
    main()
