"""Publish reviewed motherboard and ITX PSU-fit fields through the existing release pipeline.

Default: prepare and rehearse against an independent database cloned locally.
--publish: publish the exact rehearsed packet; never collect data or call models.
"""
import argparse
import hashlib
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
BASE = "e04a9a3c-d9bd-59a7-86d3-50ec11e21e9c"
PACKET = Path(__file__).resolve().parent
OUT = ROOT / "artifacts/data-board-itx-fit-20260916-r1"
CHANGED = {
    "mb-gb-b650-aorus-elite-ax",
    "psu-msi-mag-a650bn",
    "case-coolermaster-nr200p",
    "case-fractal-terra",
}


def run(*command, **kwargs):
    return subprocess.run(command, cwd=ROOT, check=True, capture_output=True, **kwargs).stdout


def write(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")


def sha(value):
    return hashlib.sha256(value.encode("utf-8")).hexdigest()


def evidence(capture, sku, field, value, excerpt):
    raw_sha = sha(json.dumps(capture, ensure_ascii=False, sort_keys=True, separators=(",", ":")))
    identity = {"source_id": capture["source_id"], "sku": sku, "field": field, "value": value, "raw_sha256": raw_sha}
    return {
        "schema_version": 1,
        "id": sha(json.dumps(identity, ensure_ascii=False, sort_keys=True, separators=(",", ":"))),
        "sku": sku,
        "field": field,
        "value": value,
        "source_id": capture["source_id"],
        "source_url": capture["source_url"],
        "captured_at": "2026-09-16T06:30:00+08:00",
        "raw_sha256": raw_sha,
        "method": "deterministic",
        "evidence_excerpt": excerpt,
        "evidence_status": "verified",
    }


def prepare(paths):
    if OUT.exists():
        return
    (OUT / "parts").mkdir(parents=True)
    shutil.copytree(paths.releases / BASE / "parts", OUT / "parts", dirs_exist_ok=True)
    shutil.copyfile(paths.releases / BASE / "evidence/fields.jsonl", OUT / "fields.jsonl")
    captures_raw = json.loads((PACKET / "official-captures.json").read_text(encoding="utf-8"))["captures"]
    captures = {item["source_id"]: item for item in captures_raw}
    changes = {
        "mb-gb-b650-aorus-elite-ax": ("motherboard", {"memory_speed_max_mts": 8000}, {
            "memory_speed_max_mts": "gigabyte-b650-aorus-elite-ax-rev12_manual_official",
        }),
        "psu-msi-mag-a650bn": ("psu", {"form_factor": "atx", "length_mm": 140}, {
            "form_factor": "msi-mag-a650bn_manual_official", "length_mm": "msi-mag-a650bn_manual_official",
        }),
        "case-coolermaster-nr200p": ("case", {"supported_psu_form_factors": ["sfx", "sfx_l"], "psu_length_max_mm": 130}, {
            "supported_psu_form_factors": "coolermaster-nr200p_manual_official", "psu_length_max_mm": "coolermaster-nr200p_manual_official",
        }),
        "case-fractal-terra": ("case", {"supported_psu_form_factors": ["sfx", "sfx_l"], "psu_length_max_mm": 130}, {
            "supported_psu_form_factors": "fractal-terra_manual_official", "psu_length_max_mm": "fractal-terra_manual_official",
        }),
    }
    for sku, (category, specs, sources) in changes.items():
        path = OUT / "parts" / f"{category}.jsonl"
        rows = []
        for line in path.read_text(encoding="utf-8").splitlines():
            row = json.loads(line)
            if row["sku"] == sku:
                row["specs"].update(specs)
                row["source_meta"].update(sources)
            rows.append(json.dumps(row, ensure_ascii=False, sort_keys=True))
        path.write_text("\n".join(rows) + "\n", encoding="utf-8", newline="\n")

    added = []
    for source_id in ("gigabyte-b650-aorus-elite-ax-rev12_manual_official", "gigabyte-b650-aorus-elite-ax-rev10-11_manual_official"):
        c = captures[source_id]
        added.append(evidence(c, "mb-gb-b650-aorus-elite-ax", "specs.memory_speed_max_mts", 8000,
                              "官网列出 DDR5 8000(OC)；实际支持取决于 CPU、内存配置及支持列表。"))
    for sku, source_id, fields in (
        ("psu-msi-mag-a650bn", "msi-mag-a650bn_manual_official", (("specs.form_factor", "atx", "PSU Form Factor: ATX"), ("specs.length_mm", 140, "Dimension: 150mm x 140mm x 86mm；安装长度 140mm"))),
        ("case-coolermaster-nr200p", "coolermaster-nr200p_manual_official", (("specs.supported_psu_form_factors", ["sfx", "sfx_l"], "PSU Support: SFX, SFX-L"), ("specs.psu_length_max_mm", 130, "PSU Clearance: 130mm"))),
        ("case-fractal-terra", "fractal-terra_manual_official", (("specs.supported_psu_form_factors", ["sfx", "sfx_l"], "PSU compatibility: SFX, SFX-L"), ("specs.psu_length_max_mm", 130, "Maximum PSU length: 130 mm"))),
    ):
        for field, value, excerpt in fields:
            added.append(evidence(captures[source_id], sku, field, value, excerpt))
    with (OUT / "fields.jsonl").open("a", encoding="utf-8", newline="\n") as stream:
        for item in sorted(added, key=lambda x: (x["sku"], x["field"], x["source_id"])):
            stream.write(json.dumps(item, ensure_ascii=False, sort_keys=True, separators=(",", ":")) + "\n")
    review = create_review(run_id=str(uuid.uuid4()), candidate_parts=OUT / "parts", base_parts=paths.releases / BASE / "parts",
                           base_release_id=BASE, candidate_evidence=OUT / "fields.jsonl", base_evidence=paths.releases / BASE / "evidence/fields.jsonl")
    assert {item["sku"] for item in review["changes"]} == CHANGED
    assert not review["risks"], review["risks"]
    write(OUT / "review.json", review)


def query(database, sql):
    return json.loads(run("docker", "exec", CONTAINER, "psql", "-XqAt", "-v", "ON_ERROR_STOP=1", "-U", DATABASE, "-d", database, "-c", sql))


def snapshot(database):
    return query(database, """SELECT json_build_object(
        'parts', (SELECT json_agg(json_build_object('sku',sku,'active',active,'catalog_state',catalog_state,'specs',specs,'source_meta',source_meta) ORDER BY sku) FROM parts),
        'snapshot_id', (SELECT id FROM price_snapshots ORDER BY snapshot_date DESC,id DESC LIMIT 1),
        'priced', (SELECT json_agg(sku ORDER BY sku) FROM prices WHERE snapshot_id=(SELECT id FROM price_snapshots ORDER BY snapshot_date DESC,id DESC LIMIT 1)),
        'prices_md5', (SELECT md5(json_agg(p ORDER BY snapshot_id,sku)::text) FROM prices p),
        'builds_md5', (SELECT md5(coalesce(json_agg(b ORDER BY id)::text,'')) FROM builds b),
        'requirements_md5', (SELECT md5(coalesce(json_agg(r ORDER BY id)::text,'')) FROM requirements r),
        'proposals_md5', (SELECT md5(coalesce(json_agg(p ORDER BY session_id)::text,'')) FROM session_proposals p))""")


def verify(before, after):
    assert before.keys() == after.keys()
    assert all(before[k] == after[k] for k in before if k != "parts"), "quotes or user history changed"
    old = {p["sku"]: p for p in before["parts"]}
    new = {p["sku"]: p for p in after["parts"]}
    assert old.keys() == new.keys()
    assert {sku for sku in old if old[sku] != new[sku]} == CHANGED
    assert new["mb-gb-b650-aorus-elite-ax"]["specs"]["memory_speed_max_mts"] == 8000
    assert new["psu-msi-mag-a650bn"]["specs"]["form_factor"] == "atx"
    assert new["psu-msi-mag-a650bn"]["specs"]["length_mm"] == 140
    assert new["case-coolermaster-nr200p"]["specs"]["supported_psu_form_factors"] == ["sfx", "sfx_l"]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--publish", action="store_true")
    args = parser.parse_args()
    paths = DataPaths.defaults()
    current = json.loads(paths.current.read_text(encoding="utf-8"))
    prepare(paths)
    review = json.loads((OUT / "review.json").read_text(encoding="utf-8"))
    if args.publish:
        rehearsal = json.loads((OUT / "rehearsal.json").read_text(encoding="utf-8"))
        assert rehearsal["verified"] and rehearsal["base_release"] == BASE
        if current["release_id"] == rehearsal["release_id"]:
            print("Already published; no changes")
            return
        assert current["release_id"] == BASE
    else:
        assert current["release_id"] == BASE
    before = snapshot(DATABASE)
    assert len(before["priced"]) == 123
    write(OUT / ("production-before.json" if args.publish else "before.json"), before)
    database = DATABASE if args.publish else "fit_eval_" + uuid.uuid4().hex[:12]
    original_dsn = os.environ.get("PG_DSN")
    try:
        if not args.publish:
            run("docker", "exec", CONTAINER, "createdb", "-U", DATABASE, database)
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
        release = publish_reviewed_release(paths, run_id=review["run_id"], candidate_parts=OUT / "parts",
                                           candidate_evidence=OUT / "fields.jsonl", review=review, policy="manual")
        after = snapshot(database)
        verify(before, after)
        write(OUT / ("production-after.json" if args.publish else "after.json"), after)
        result = {"verified": True, "base_release": BASE, "release_id": release["release_id"], "changed_skus": sorted(CHANGED),
                  "active": 123, "priced": 123, "history_unchanged": True, "model_requests": 0, "external_price_requests": 0}
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
