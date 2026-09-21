"""P11/P12 `pcdata` 统一命令入口。"""

from __future__ import annotations

import argparse
import json
import sys
import uuid
from datetime import datetime
from pathlib import Path
from typing import Any

from .automation import (
    CheckpointStore,
    DataPaths,
    HTTPCollector,
    PipelineError,
    _copy_parts,
    _collect_amd_cpu_source,
    _current_evidence,
    _current_parts,
    _load_evidence,
    _load_parts,
    _parts_digest,
    _write_evidence,
    _write_review,
    bootstrap_release,
    create_review,
    health_report,
    locked_source_result,
    publish_reviewed_release,
)
from .canonical import SpecError
from .registry import DEFAULT_REGISTRY_PATH, load_registry
from .prices import (
    create_price_review,
    import_price_csv,
    price_health,
    publish_price_review,
)
from .serpapi_baidu import collect_serpapi_baidu


def _json(value: Any) -> None:
    json.dump(value, sys.stdout, ensure_ascii=False, sort_keys=True, indent=2)
    print()


def _parse_datetime(value: str) -> datetime:
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise argparse.ArgumentTypeError("必须为 RFC 3339 时间") from exc
    if parsed.tzinfo is None:
        raise argparse.ArgumentTypeError("时间必须包含时区")
    return parsed


def _paths(args: argparse.Namespace) -> DataPaths:
    defaults = DataPaths.defaults()
    runtime = Path(args.runtime_root).resolve() if args.runtime_root else defaults.runtime_root
    return DataPaths(defaults.repo_root, defaults.data_root, runtime)


def _cmd_source(args: argparse.Namespace) -> int:
    sources = load_registry(Path(args.registry))
    paths = _paths(args)
    lock_errors: list[dict[str, str]] = []
    result: dict[str, Any] = {
        "schema_version": 1,
        "sources": [
            {
                "id": source["id"],
                "enabled": source["enabled"],
                "adapter": source["adapter"],
                "schedule": source["schedule"],
                "automated_access": source["automated_access"],
                "status": "disabled" if not source["enabled"] else "configured",
            }
            for source in sources.values()
        ],
    }
    for item in result["sources"]:
        source = sources[item["id"]]
        if source["enabled"] and source["adapter"] in {"locked_git_version", "locked_package"}:
            try:
                item.update(locked_source_result(source, paths.data_root))
            except PipelineError as exc:
                item.update({"status": "blocked", "error_code": exc.code})
                lock_errors.append({"source_id": source["id"], "error_code": exc.code})
    if args.live:
        paths.ensure()
        collector = HTTPCollector()
        checkpoints = CheckpointStore(paths.checkpoints)
        live_run = paths.runs / str(uuid.uuid4()) / "raw"
        for item in result["sources"]:
            source = sources[item["id"]]
            if source["enabled"] and source["adapter"] == "http_snapshot":
                item.update(collector.collect(source, live_run, checkpoints))
    _json(result)
    return 1 if lock_errors else 0


def _cmd_collect(args: argparse.Namespace) -> int:
    paths = _paths(args)
    paths.ensure()
    sources = load_registry(Path(args.registry))
    if args.source not in sources:
        raise PipelineError("source_not_found", f"来源 {args.source!r} 未登记")
    source = sources[args.source]
    run_id = args.run_id or str(uuid.uuid4())
    if source["adapter"] == "http_snapshot":
        result = HTTPCollector().collect(
            source,
            paths.runs / run_id / "raw",
            CheckpointStore(paths.checkpoints),
        )
    elif source["adapter"] == "amd_cpu_official":
        base_release_id, base_parts = _current_parts(paths)
        evidence = _load_evidence(_current_evidence(paths, base_release_id))
        result = _collect_amd_cpu_source(
            source=source,
            paths=paths,
            run_dir=paths.runs / run_id,
            checkpoints=CheckpointStore(paths.checkpoints),
            collector=HTTPCollector(),
            current_parts=_load_parts(base_parts or paths.seed_parts),
            evidence=evidence,
        )
        _write_evidence(paths.runs / run_id / "normalized" / "evidence.jsonl", evidence.values())
    elif source["adapter"] == "local_parts":
        result = {
            "source_id": source["id"],
            "status": "collected",
            "content_sha256": _parts_digest(paths.seed_parts),
        }
    else:
        result = locked_source_result(source, paths.data_root)
    _json({"schema_version": 1, "run_id": run_id, "result": result})
    return 0


def _cmd_normalize(args: argparse.Namespace) -> int:
    paths = _paths(args)
    target = paths.runs / args.run_id / "normalized" / "parts"
    evidence_target = paths.runs / args.run_id / "normalized" / "evidence.jsonl"
    base_release_id, base_parts = _current_parts(paths)
    _copy_parts(base_parts or paths.seed_parts, target)
    if not evidence_target.is_file():
        _write_evidence(evidence_target, _load_evidence(_current_evidence(paths, base_release_id)).values())
    _json({"schema_version": 1, "run_id": args.run_id, "parts_sha256": _parts_digest(target)})
    return 0


def _cmd_review(args: argparse.Namespace) -> int:
    paths = _paths(args)
    run_dir = paths.runs / args.run_id
    candidate = run_dir / "normalized" / "parts"
    base_release_id, base_parts = _current_parts(paths)
    candidate_evidence = run_dir / "normalized" / "evidence.jsonl"
    review = create_review(
        run_id=args.run_id,
        candidate_parts=candidate,
        base_parts=base_parts,
        base_release_id=base_release_id,
        candidate_evidence=candidate_evidence,
        base_evidence=_current_evidence(paths, base_release_id),
        model_used=False,
    )
    _write_review(run_dir, review)
    _json(review)
    return 0 if review["decision"] in {"no_change", "auto_publish"} else 1


def _cmd_publish(args: argparse.Namespace) -> int:
    paths = _paths(args)
    run_dir = paths.runs / args.run_id
    try:
        review = json.loads((run_dir / "review.json").read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise PipelineError("review_missing", f"无法读取 run {args.run_id} 的 review") from exc
    release = publish_reviewed_release(
        paths,
        run_id=args.run_id,
        candidate_parts=run_dir / "normalized" / "parts",
        candidate_evidence=run_dir / "normalized" / "evidence.jsonl",
        review=review,
        policy=args.policy,
    )
    _json(release)
    return 0


def _cmd_bootstrap(args: argparse.Namespace) -> int:
    _json(bootstrap_release(_paths(args)))
    return 0


def _cmd_health(args: argparse.Namespace) -> int:
    report = health_report(_paths(args))
    _json(report)
    return 0 if report["healthy"] else 1


def _cmd_price_import(args: argparse.Namespace) -> int:
    _json(import_price_csv(_paths(args), Path(args.file), run_id=args.run_id))
    return 0


def _cmd_price_review(args: argparse.Namespace) -> int:
    result = create_price_review(_paths(args), args.run_id)
    _json(result)
    return 1 if result["decision"] == "quarantine" else 0


def _cmd_price_publish(args: argparse.Namespace) -> int:
    _json(publish_price_review(_paths(args), args.run_id, policy=args.policy))
    return 0


def _cmd_price_health(args: argparse.Namespace) -> int:
    result = price_health(_paths(args))
    _json(result)
    return 0 if result["healthy"] else 1


def _cmd_price_collect(args: argparse.Namespace) -> int:
    result = collect_serpapi_baidu(
        _paths(args), mode=args.mode, scheduled_for=args.scheduled_for,
    )
    _json(result)
    return 0


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="pcdata", description="P11/P12 本机数据采集与安全发布")
    parser.add_argument("--runtime-root", help="覆盖本机 var/data 路径（测试/诊断用）")
    parser.add_argument("--registry", default=str(DEFAULT_REGISTRY_PATH), help="来源登记文件")
    sub = parser.add_subparsers(dest="command", required=True)

    source = sub.add_parser("source", help="来源登记操作")
    source_sub = source.add_subparsers(dest="source_command", required=True)
    check = source_sub.add_parser("check", help="校验来源登记")
    check.add_argument("--live", action="store_true", help="对已启用 HTTP 来源作最小请求")
    check.set_defaults(func=_cmd_source)

    collect = sub.add_parser("collect", help="采集一个已登记来源")
    collect.add_argument("--source", required=True)
    collect.add_argument("--run-id")
    collect.set_defaults(func=_cmd_collect)

    normalize = sub.add_parser("normalize", help="生成确定性 normalized parts")
    normalize.add_argument("--run-id", required=True)
    normalize.set_defaults(func=_cmd_normalize)

    review = sub.add_parser("review", help="比较 last-known-good 并分类风险")
    review.add_argument("--run-id", required=True)
    review.set_defaults(func=_cmd_review)

    publish = sub.add_parser("publish", help="发布已审核 run")
    publish.add_argument("--run-id", required=True)
    publish.add_argument("--policy", choices=("auto", "manual"), required=True)
    publish.set_defaults(func=_cmd_publish)

    bootstrap = sub.add_parser("bootstrap", help="建立初始 last-known-good 并导入数据库")
    bootstrap.set_defaults(func=_cmd_bootstrap)

    health = sub.add_parser("health", help="检查本机数据发布状态")
    health.set_defaults(func=_cmd_health)

    price = sub.add_parser("price", help="P12 价格观察与安全选价")
    price_sub = price.add_subparsers(dest="price_command", required=True)
    price_import = price_sub.add_parser("import", help="导入严格价格观察 CSV")
    price_import.add_argument("--file", required=True)
    price_import.add_argument("--run-id")
    price_import.set_defaults(func=_cmd_price_import)
    price_review = price_sub.add_parser("review", help="确定性选价并生成审核结果")
    price_review.add_argument("--run-id", required=True)
    price_review.set_defaults(func=_cmd_price_review)
    price_publish = price_sub.add_parser("publish", help="发布已审核价格快照")
    price_publish.add_argument("--run-id", required=True)
    price_publish.add_argument("--policy", choices=("manual", "automatic"), required=True)
    price_publish.set_defaults(func=_cmd_price_publish)
    price_health_cmd = price_sub.add_parser("health", help="检查价格发布和新鲜度")
    price_health_cmd.set_defaults(func=_cmd_price_health)
    price_collect = price_sub.add_parser("collect", help="执行 SerpApi/Baidu canary 或每日微批次")
    price_collect.add_argument("--mode", choices=("canary", "daily"), required=True)
    price_collect.add_argument("--scheduled-for", type=_parse_datetime)
    price_collect.set_defaults(func=_cmd_price_collect)
    return parser


def main(argv: list[str] | None = None) -> int:
    parser = _parser()
    args = parser.parse_args(argv)
    try:
        return int(args.func(args))
    except (PipelineError, SpecError, OSError, json.JSONDecodeError) as exc:
        code = exc.code if isinstance(exc, PipelineError) else "invalid_data"
        _json({"schema_version": 1, "error": {"code": code, "message": str(exc)}})
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
