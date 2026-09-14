# -*- coding: utf-8 -*-
"""2026-09-14 旧SKU基线刷新批次:预算化 search/detail 驱动(checkpoint 幂等,可续跑)。

预算(任务提示词首阶段上限,失败与重试均计入):
  search ≤200 次调用(每次 1 个 HTTP 请求)、detail ≤120 次(每次 2 个 HTTP 请求);
  单个查询最多 5 页;串行执行(并发 1 ≤ 上限 2),调用间 0.7s。

用法(cd scripts/data/collection-tools/2026-09-13):
  python expand_driver.py search --plan search_plan.jsonl   # 执行待跑查询,产出原始 CSV
  python expand_driver.py detail --ids detail_ids.jsonl     # 执行待跑详情,产出原始 YAML
  python expand_driver.py status                            # 查看预算消耗

原始数据与 checkpoint 在 var/data/collections/2026-09-14-legacy-refresh-01/(不入库)。
"""
from __future__ import annotations

import argparse
import csv
import hashlib
import io
import json
import os
import subprocess
import sys
import time
from datetime import datetime, timedelta, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parents[4]
RAW = Path(
    os.environ.get(
        "COLLECTION_DATA_DIR",
        ROOT / "var/data/collections/2026-09-14-legacy-refresh-01",
    )
).resolve()
SKILL = Path(os.environ.get("MAISHOU_SKILL_DIR", r"C:\Users\83818\.qoder\skills\taobao"))

BUDGET_SEARCH = 200
BUDGET_DETAIL = 120
MAX_PAGES = 5
SLEEP_SECONDS = 0.7

SEARCH_CSV = RAW / "raw" / "search"
DETAIL_RAW = RAW / "raw" / "detail"
CHECKPOINT = RAW / "checkpoint.json"
RAW_INDEX = RAW / "raw_index.jsonl"


def now() -> str:
    return datetime.now(timezone(timedelta(hours=8))).isoformat(timespec="seconds")


def load_checkpoint() -> dict:
    if CHECKPOINT.exists():
        return json.loads(CHECKPOINT.read_text(encoding="utf-8"))
    return {
        "batch": "2026-09-14-legacy-refresh-01",
        "search_count": 0,
        "detail_count": 0,
        "http_requests": 0,
        "queries": {},  # query_id -> {"pages_done": n, "rows_total": m}
        "details": {},  # detail_id -> {"file": ..., "status": ok/fail}
        "history": [],  # 每次调用的流水(含失败)
    }


def save_checkpoint(cp: dict) -> None:
    CHECKPOINT.parent.mkdir(parents=True, exist_ok=True)
    CHECKPOINT.write_text(json.dumps(cp, ensure_ascii=False, indent=1), encoding="utf-8")


def append_index(record: dict) -> None:
    RAW_INDEX.parent.mkdir(parents=True, exist_ok=True)
    with RAW_INDEX.open("a", encoding="utf-8") as f:
        f.write(json.dumps(record, ensure_ascii=False) + "\n")


def sha256_of(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def run_skill(args: list[str], timeout: int = 90) -> tuple[int, str, str]:
    proc = subprocess.run(
        ["uv", "run", "scripts/main.py", *args],
        cwd=SKILL,
        capture_output=True,
        timeout=timeout,
    )
    return (
        proc.returncode,
        proc.stdout.decode("utf-8", "replace"),
        proc.stderr.decode("utf-8", "replace"),
    )


def do_search(cp: dict, q: dict) -> None:
    """执行一个查询的待跑页;每页一次调用,失败也计入预算。"""
    qid = q["query_id"]
    state = cp["queries"].setdefault(qid, {"pages_done": 0, "rows_total": 0, "keywords": []})
    max_pages = min(int(q.get("max_pages", 1)), MAX_PAGES)
    while state["pages_done"] < max_pages:
        if cp["search_count"] >= BUDGET_SEARCH:
            print(f"[budget] search 上限 {BUDGET_SEARCH} 已达,停止。", flush=True)
            return
        page = state["pages_done"] + 1
        call_ts = now()
        cp["search_count"] += 1
        cp["http_requests"] += 1
        cp["history"].append({"t": call_ts, "kind": "search", "query_id": qid, "page": page})
        try:
            rc, out, err = run_skill([
                "search",
                f"--source={q.get('source', 0)}",
                f"--keyword={q['keyword']}",
                f"--page={page}",
            ])
        except subprocess.TimeoutExpired:
            err = "timeout 90s"
            rc, out = 99, ""
        fname = f"{qid}__s{q.get('source', 0)}__p{page}.csv"
        fpath = SEARCH_CSV / fname
        fpath.parent.mkdir(parents=True, exist_ok=True)
        rows = 0
        ok = False
        if rc == 0 and not out.lstrip().startswith("错误") and "," in out:
            fpath.write_text(out, encoding="utf-8")
            rows = max(0, len(list(csv.DictReader(io.StringIO(out)))))
            ok = True
        else:
            fpath.write_text(
                f"# FAILED rc={rc} ts={call_ts}\n# stderr: {err.strip()[-400:]}\n{out}",
                encoding="utf-8",
            )
        state["pages_done"] = page
        state["rows_total"] += rows
        cp["history"][-1].update({"ok": ok, "rows": rows, "file": str(fpath.relative_to(RAW))})
        append_index({
            "kind": "search", "query_id": qid, "keyword": q["keyword"],
            "source": q.get("source", 0), "page": page, "ts": call_ts, "ok": ok,
            "rows": rows, "file": str(fpath.relative_to(RAW)), "sha256": sha256_of(fpath),
        })
        print(f"[{cp['search_count']}/{BUDGET_SEARCH}] {'OK ' if ok else 'FAIL'} {qid} p{page} rows={rows} {q['keyword']}", flush=True)
        save_checkpoint(cp)
        time.sleep(SLEEP_SECONDS)
        if ok and rows == 0:
            break  # 空页停止该查询(内容重复/无新增按提示词停止并留痕)


def do_detail(cp: dict, d: dict) -> None:
    did = d["detail_id"]
    if did in cp["details"] and cp["details"][did].get("status") == "ok":
        return
    if cp["detail_count"] >= BUDGET_DETAIL:
        print(f"[budget] detail 上限 {BUDGET_DETAIL} 已达,停止。", flush=True)
        return
    call_ts = now()
    cp["detail_count"] += 1
    cp["http_requests"] += 2
    cp["history"].append({"t": call_ts, "kind": "detail", "detail_id": did})
    try:
        rc, out, err = run_skill([
            "detail", f"--source={d.get('source', 1)}", f"--id={d['goodsId']}",
        ])
    except subprocess.TimeoutExpired:
        err = "timeout 90s"
        rc, out = 99, ""
    fname = f"{did}__s{d.get('source', 1)}.yaml"
    fpath = DETAIL_RAW / fname
    fpath.parent.mkdir(parents=True, exist_ok=True)
    ok = rc == 0 and "购买链接" in out
    if ok:
        fpath.write_text(out, encoding="utf-8")
    else:
        fpath.write_text(
            f"# FAILED rc={rc} ts={call_ts}\n# stderr: {err.strip()[-400:]}\n{out}",
            encoding="utf-8",
        )
    cp["details"][did] = {"status": "ok" if ok else "fail", "file": str(fpath.relative_to(RAW))}
    cp["history"][-1].update({"ok": ok, "file": str(fpath.relative_to(RAW))})
    append_index({
        "kind": "detail", "detail_id": did, "goodsId": d.get("goodsId"),
        "source": d.get("source", 1), "ts": call_ts, "ok": ok,
        "file": str(fpath.relative_to(RAW)), "sha256": sha256_of(fpath),
    })
    print(f"[{cp['detail_count']}/{BUDGET_DETAIL}] {'OK ' if ok else 'FAIL'} {did}", flush=True)
    save_checkpoint(cp)
    time.sleep(SLEEP_SECONDS)


def cmd_search(plan_path: Path) -> int:
    cp = load_checkpoint()
    queries = [json.loads(l) for l in plan_path.read_text(encoding="utf-8").splitlines() if l.strip()]
    for q in queries:
        st = cp["queries"].get(q["query_id"], {})
        if st.get("pages_done", 0) >= min(int(q.get("max_pages", 1)), MAX_PAGES):
            continue
        do_search(cp, q)
    print(json.dumps({"search": cp["search_count"], "detail": cp["detail_count"], "http": cp["http_requests"]}))
    return 0


def cmd_detail(ids_path: Path) -> int:
    cp = load_checkpoint()
    items = [json.loads(l) for l in ids_path.read_text(encoding="utf-8").splitlines() if l.strip()]
    for d in items:
        do_detail(cp, d)
    print(json.dumps({"search": cp["search_count"], "detail": cp["detail_count"], "http": cp["http_requests"]}))
    return 0


def cmd_status() -> int:
    cp = load_checkpoint()
    print(json.dumps({
        "search": f"{cp['search_count']}/{BUDGET_SEARCH}",
        "detail": f"{cp['detail_count']}/{BUDGET_DETAIL}",
        "http_requests": cp["http_requests"],
        "queries_done": len(cp["queries"]),
        "details": len(cp["details"]),
    }, ensure_ascii=False))
    return 0


def main() -> int:
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(required=True)
    sp = sub.add_parser("search")
    sp.add_argument("--plan", required=True)
    dp = sub.add_parser("detail")
    dp.add_argument("--ids", required=True)
    sub.add_parser("status")
    args = ap.parse_args()
    if args.__dict__.get("plan"):
        return cmd_search(Path(args.plan))
    if args.__dict__.get("ids"):
        return cmd_detail(Path(args.ids))
    return cmd_status()


if __name__ == "__main__":
    sys.exit(main())
