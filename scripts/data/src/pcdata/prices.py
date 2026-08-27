"""P12A 价格观察、确定性选价和不可变发布。

模块只处理人工 CSV 和已获准适配器产出的结构化观察，不包含搜索、浏览器或
模型入口。历史四列 CSV 由 Go 兼容导入器处理，不能升级为已验证 observation。
"""

from __future__ import annotations

import csv
import hashlib
import json
import math
import os
import shutil
import subprocess
import uuid
from collections import defaultdict
from datetime import UTC, date, datetime
from decimal import Decimal, InvalidOperation
from pathlib import Path
from typing import Any, Iterable
from urllib.parse import urlsplit

from .automation import DataPaths, PipelineError, RunLock, sha256_file, stable_json_bytes
from .coverage import load_parts_jsonl

PRICE_RELEASE_SCHEMA_VERSION = 1
PRICE_CSV_HEADER = [
    "sku",
    "price_cny",
    "currency",
    "source_id",
    "product_id",
    "source_url",
    "seller",
    "price_type",
    "stock_status",
    "variant_match",
    "observed_at",
    "raw_sha256",
]
QUALIFIED_PRICE_TYPES = {"regular", "sale"}
OBSERVED_ONLY_PRICE_TYPES = {"coupon", "member", "msrp"}
REJECTED_PRICE_TYPES = {"deposit", "installment", "bundle", "unknown"}
ALL_PRICE_TYPES = QUALIFIED_PRICE_TYPES | OBSERVED_ONLY_PRICE_TYPES | REJECTED_PRICE_TYPES
STOCK_STATUSES = {"in_stock", "out_of_stock", "unknown"}
VARIANT_MATCHES = {"exact", "mismatch", "unknown"}
SOURCE_PRIORITY = {"official_store": 0, "authorized_retail": 10, "manual": 50}


def _now() -> datetime:
    return datetime.now(UTC)


def _rfc3339(value: datetime | None = None) -> str:
    return (value or _now()).astimezone(UTC).isoformat().replace("+00:00", "Z")


def _atomic_write(path: Path, data: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(f".{path.name}.{uuid.uuid4().hex}.tmp")
    temporary.write_bytes(data)
    os.replace(temporary, path)


def _atomic_json(path: Path, value: Any) -> None:
    _atomic_write(path, stable_json_bytes(value))


def _sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def _price_runs(paths: DataPaths) -> Path:
    return paths.data_root / "work" / "price-runs"


def _price_releases(paths: DataPaths) -> Path:
    return paths.runtime_root / "price-releases"


def _current_price(paths: DataPaths) -> Path:
    return paths.runtime_root / "current-price.json"


def _price_inbox(paths: DataPaths) -> Path:
    return paths.runtime_root / "price-inbox"


def _ensure(paths: DataPaths) -> None:
    paths.ensure()
    for target in (_price_runs(paths), _price_releases(paths), _price_inbox(paths)):
        target.mkdir(parents=True, exist_ok=True)


def _active_core_skus(paths: DataPaths) -> set[str]:
    """读取 P11 current parts；没有发布时使用仓库 seed。"""
    parts_dir = paths.seed_parts
    try:
        current = json.loads(paths.current.read_text(encoding="utf-8"))
        release_id = str(current["release_id"])
        candidate = paths.releases / release_id / "parts"
        if candidate.is_dir():
            parts_dir = candidate
    except (OSError, KeyError, TypeError, json.JSONDecodeError):
        pass
    records: list[dict[str, Any]] = []
    for path in sorted(parts_dir.glob("*.jsonl")):
        records.extend(load_parts_jsonl(path))
    return {
        str(record["sku"])
        for record in records
        if record.get("catalog_state", "active_core") == "active_core"
    }


def _parse_decimal(value: str, *, row_number: int) -> Decimal:
    try:
        parsed = Decimal(value)
    except InvalidOperation as exc:
        raise PipelineError("invalid_price_csv", f"第 {row_number} 行 price_cny 非法") from exc
    if parsed <= 0 or parsed.as_tuple().exponent < -2 or parsed >= Decimal("100000000"):
        raise PipelineError("invalid_price_csv", f"第 {row_number} 行 price_cny 必须为正数且最多两位小数")
    return parsed.quantize(Decimal("0.01"))


def _parse_time(value: str, *, row_number: int) -> datetime:
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise PipelineError("invalid_price_csv", f"第 {row_number} 行 observed_at 必须为 RFC 3339") from exc
    if parsed.tzinfo is None:
        raise PipelineError("invalid_price_csv", f"第 {row_number} 行 observed_at 必须包含时区")
    if parsed > _now().replace(microsecond=0):
        raise PipelineError("invalid_price_csv", f"第 {row_number} 行 observed_at 不得在未来")
    return parsed.astimezone(UTC)


def _validate_url(value: str, *, row_number: int) -> None:
    parsed = urlsplit(value)
    if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password:
        raise PipelineError("invalid_price_csv", f"第 {row_number} 行 source_url 必须为不含凭据的 HTTPS URL")


def _observation_identity(row: dict[str, Any]) -> str:
    identity = {
        "sku": row["sku"],
        "source_id": row["source_id"],
        "product_id": row["product_id"],
        "price_cny": row["price_cny"],
        "observed_at": row["observed_at"],
        "raw_sha256": row["raw_sha256"],
    }
    return _sha256_bytes(json.dumps(identity, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode())


def load_price_csv(path: Path, active_skus: set[str]) -> list[dict[str, Any]]:
    """严格解析 P12 CSV，并为每条记录产生确定性裁决和 observation ID。"""
    try:
        with path.open("r", encoding="utf-8-sig", newline="") as stream:
            reader = csv.DictReader(stream)
            if reader.fieldnames != PRICE_CSV_HEADER:
                raise PipelineError("invalid_price_csv", f"表头必须严格为 {PRICE_CSV_HEADER}")
            raw_rows = list(reader)
    except UnicodeDecodeError as exc:
        raise PipelineError("invalid_price_csv", "价格 CSV 必须为 UTF-8") from exc
    if not raw_rows:
        raise PipelineError("invalid_price_csv", "价格 CSV 没有数据行")

    observations: list[dict[str, Any]] = []
    seen: set[str] = set()
    for row_number, raw in enumerate(raw_rows, start=2):
        if any(value is None for value in raw.values()):
            raise PipelineError("invalid_price_csv", f"第 {row_number} 行列数不正确")
        sku = raw["sku"].strip()
        source_id = raw["source_id"].strip()
        product_id = raw["product_id"].strip()
        seller = raw["seller"].strip()
        if not sku or not source_id or not product_id or not seller:
            raise PipelineError("invalid_price_csv", f"第 {row_number} 行 SKU、来源、商品和商家不得为空")
        if sku not in active_skus:
            raise PipelineError("invalid_price_csv", f"第 {row_number} 行 SKU {sku!r} 不是 active_core")
        price = _parse_decimal(raw["price_cny"].strip(), row_number=row_number)
        if raw["currency"].strip() != "CNY":
            raise PipelineError("invalid_price_csv", f"第 {row_number} 行只允许 CNY")
        source_url = raw["source_url"].strip()
        _validate_url(source_url, row_number=row_number)
        price_type = raw["price_type"].strip()
        stock_status = raw["stock_status"].strip()
        variant_match = raw["variant_match"].strip()
        if price_type not in ALL_PRICE_TYPES or stock_status not in STOCK_STATUSES or variant_match not in VARIANT_MATCHES:
            raise PipelineError("invalid_price_csv", f"第 {row_number} 行价格类型、库存或变体枚举非法")
        observed_at = _parse_time(raw["observed_at"].strip(), row_number=row_number)
        raw_sha256 = raw["raw_sha256"].strip()
        if len(raw_sha256) != 64 or any(ch not in "0123456789abcdef" for ch in raw_sha256):
            raise PipelineError("invalid_price_csv", f"第 {row_number} 行 raw_sha256 非法")

        reasons: list[str] = []
        if stock_status != "in_stock":
            reasons.append("not_in_stock")
        if variant_match != "exact":
            reasons.append("variant_not_exact")
        if price_type in REJECTED_PRICE_TYPES:
            reasons.append("price_type_rejected")
        if reasons:
            status = "rejected"
        elif price_type in OBSERVED_ONLY_PRICE_TYPES:
            status = "observed_only"
        else:
            status = "qualified"
        row = {
            "schema_version": 1,
            "sku": sku,
            "price_cny": f"{price:.2f}",
            "currency": "CNY",
            "source_id": source_id,
            "product_id": product_id,
            "source_url": source_url,
            "seller": seller,
            "price_type": price_type,
            "stock_status": stock_status,
            "variant_match": variant_match,
            "observed_at": _rfc3339(observed_at),
            "raw_sha256": raw_sha256,
            "decision_status": status,
            "rejection_reasons": reasons,
        }
        row["observation_id"] = _observation_identity(row)
        if row["observation_id"] in seen:
            raise PipelineError("invalid_price_csv", f"第 {row_number} 行 observation 重复")
        seen.add(row["observation_id"])
        observations.append(row)
    return observations


def _write_jsonl(path: Path, rows: Iterable[dict[str, Any]]) -> None:
    data = b"".join(
        (json.dumps(row, ensure_ascii=False, sort_keys=True, separators=(",", ":")) + "\n").encode("utf-8")
        for row in rows
    )
    _atomic_write(path, data)


def _load_jsonl(path: Path) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    try:
        for line_number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), start=1):
            if line.strip():
                value = json.loads(line)
                if not isinstance(value, dict):
                    raise ValueError("record is not object")
                rows.append(value)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        raise PipelineError("invalid_price_artifact", f"无法读取 {path.name} 的第 {locals().get('line_number', 0)} 行") from exc
    return rows


def import_price_csv(paths: DataPaths, csv_path: Path, *, run_id: str | None = None) -> dict[str, Any]:
    _ensure(paths)
    run_id = run_id or str(uuid.uuid4())
    try:
        uuid.UUID(run_id)
    except ValueError as exc:
        raise PipelineError("invalid_run_id", "run_id 必须为 UUID") from exc
    observations = load_price_csv(csv_path, _active_core_skus(paths))
    target = _price_runs(paths) / run_id / "normalized" / "observations.jsonl"
    _write_jsonl(target, sorted(observations, key=lambda item: (item["sku"], item["source_id"], item["observation_id"])))
    manifest = {
        "schema_version": 1,
        "run_id": run_id,
        "status": "imported",
        "model_used": False,
        "source_file_sha256": sha256_file(csv_path),
        "observations_sha256": sha256_file(target),
        "stats": {
            "total": len(observations),
            "qualified": sum(item["decision_status"] == "qualified" for item in observations),
            "observed_only": sum(item["decision_status"] == "observed_only" for item in observations),
            "rejected": sum(item["decision_status"] == "rejected" for item in observations),
        },
    }
    _atomic_json(_price_runs(paths) / run_id / "import.json", manifest)
    return manifest


def _read_current(paths: DataPaths) -> tuple[dict[str, Any] | None, list[dict[str, Any]]]:
    current_path = _current_price(paths)
    if not current_path.is_file():
        legacy_files = sorted(paths.seed_prices.glob("*.csv"))
        if not legacy_files:
            return None, []
        latest = legacy_files[-1]
        rows: list[dict[str, Any]] = []
        with latest.open("r", encoding="utf-8-sig", newline="") as stream:
            reader = csv.DictReader(stream)
            if reader.fieldnames != ["sku", "price_cny", "source", "captured_at"]:
                raise PipelineError("legacy_price_invalid", f"历史价格文件 {latest.name} 表头非法")
            for item in reader:
                rows.append({
                    "schema_version": 1,
                    "sku": item["sku"],
                    "observation_id": None,
                    "price_cny": f"{Decimal(item['price_cny']):.2f}",
                    "source_id": f"legacy:{item['source']}",
                    "observed_at": f"{item['captured_at']}T00:00:00Z",
                    "price_type": "bootstrap",
                    "carried_forward": True,
                    "source_snapshot_date": item["captured_at"],
                })
        return {
            "release_id": None,
            "snapshot_date": latest.stem,
            "policy": "bootstrap",
        }, rows
    try:
        current = json.loads(current_path.read_text(encoding="utf-8"))
        release_id = str(current["release_id"])
        release_dir = _price_releases(paths) / release_id
        manifest = json.loads((release_dir / "manifest.json").read_text(encoding="utf-8"))
        if manifest.get("manifest_sha256") != current.get("manifest_sha256"):
            raise ValueError("manifest mismatch")
        return manifest, _load_jsonl(release_dir / "selection.jsonl")
    except (OSError, KeyError, TypeError, ValueError, json.JSONDecodeError) as exc:
        raise PipelineError("current_price_corrupt", "current-price.json 或价格 release 已损坏") from exc


def _priority(item: dict[str, Any]) -> tuple[Any, ...]:
    return (
        SOURCE_PRIORITY.get(str(item["source_id"]), 100),
        0 if item["price_type"] == "regular" else 1,
        str(item["observed_at"]),
        Decimal(str(item["price_cny"])),
        str(item["observation_id"]),
    )


def _select_candidate(items: list[dict[str, Any]]) -> dict[str, Any]:
    sources = {str(item["source_id"]) for item in items}
    if len(sources) < 3:
        return min(items, key=_priority)
    values = sorted(Decimal(str(item["price_cny"])) for item in items)
    middle = len(values) // 2
    median = values[middle] if len(values) % 2 else (values[middle - 1] + values[middle]) / 2
    return min(items, key=lambda item: (abs(Decimal(str(item["price_cny"])) - median), *_priority(item)))


def create_price_review(paths: DataPaths, run_id: str) -> dict[str, Any]:
    run_dir = _price_runs(paths) / run_id
    observations_path = run_dir / "normalized" / "observations.jsonl"
    observations = _load_jsonl(observations_path)
    previous_manifest, previous_rows = _read_current(paths)
    previous = {str(row["sku"]): row for row in previous_rows}
    grouped: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for item in observations:
        if item.get("decision_status") == "qualified":
            grouped[str(item["sku"])].append(item)

    provisional: dict[str, dict[str, Any]] = {}
    quarantined: list[dict[str, Any]] = []
    for sku, items in grouped.items():
        chosen = _select_candidate(items)
        prior = previous.get(sku)
        if prior is not None:
            old = Decimal(str(prior["price_cny"]))
            new = Decimal(str(chosen["price_cny"]))
            if abs(new - old) / old > Decimal("0.25"):
                quarantined.append({"sku": sku, "reason": "price_change_over_25_percent", "before": f"{old:.2f}", "after": f"{new:.2f}"})
                continue
        provisional[sku] = chosen

    systemic_threshold = max(3, math.ceil(max(1, len(grouped)) * 0.10))
    systemic = len(quarantined) >= systemic_threshold
    if systemic:
        provisional.clear()

    selection: list[dict[str, Any]] = []
    all_skus = sorted(set(previous) | set(provisional))
    for sku in all_skus:
        chosen = provisional.get(sku)
        if chosen is not None:
            selection.append({
                "schema_version": 1,
                "sku": sku,
                "observation_id": chosen["observation_id"],
                "price_cny": chosen["price_cny"],
                "source_id": chosen["source_id"],
                "observed_at": chosen["observed_at"],
                "price_type": chosen["price_type"],
                "carried_forward": False,
                "source_snapshot_date": None,
            })
        elif sku in previous:
            carried = dict(previous[sku])
            carried["carried_forward"] = True
            carried["source_snapshot_date"] = carried.get("source_snapshot_date") or (
                previous_manifest.get("snapshot_date") if previous_manifest else None
            )
            selection.append(carried)

    selection_path = run_dir / "selection.jsonl"
    _write_jsonl(selection_path, selection)
    changed = [
        row for row in selection
        if row["sku"] not in previous
        or row["price_cny"] != previous[row["sku"]]["price_cny"]
        or row["observed_at"] != previous[row["sku"]]["observed_at"]
    ]
    review = {
        "schema_version": 1,
        "run_id": run_id,
        "base_release_id": previous_manifest.get("release_id") if previous_manifest else None,
        "decision": "quarantine" if systemic else ("publish" if changed else "no_change"),
        "snapshot_date": _now().date().isoformat(),
        "observations_sha256": sha256_file(observations_path),
        "selection_sha256": sha256_file(selection_path),
        "qualified_skus": len(grouped),
        "changed_skus": [row["sku"] for row in changed],
        "quarantined": quarantined,
        "systemic_threshold": systemic_threshold,
        "systemic_quarantine": systemic,
        "model_used": False,
    }
    _atomic_json(run_dir / "review.json", review)
    return review


def _manifest_with_hash(value: dict[str, Any]) -> dict[str, Any]:
    result = dict(value)
    result["manifest_sha256"] = ""
    result["manifest_sha256"] = _sha256_bytes(stable_json_bytes(result))
    return result


def _stage_price_release(paths: DataPaths, run_id: str, review: dict[str, Any], policy: str) -> tuple[Path, dict[str, Any]]:
    run_dir = _price_runs(paths) / run_id
    observations = run_dir / "normalized" / "observations.jsonl"
    selection = run_dir / "selection.jsonl"
    review_path = run_dir / "review.json"
    input_sha = _sha256_bytes(stable_json_bytes({
        "observations_sha256": sha256_file(observations),
        "selection_sha256": sha256_file(selection),
        "review_sha256": sha256_file(review_path),
    }))
    release_id = str(uuid.uuid5(uuid.NAMESPACE_URL, f"pcdata-price:{run_id}:{input_sha}:{policy}"))
    final_dir = _price_releases(paths) / release_id
    if final_dir.exists():
        return final_dir, json.loads((final_dir / "manifest.json").read_text(encoding="utf-8"))
    temporary = _price_releases(paths) / f".{release_id}.tmp"
    if temporary.exists():
        shutil.rmtree(temporary)
    temporary.mkdir(parents=True)
    for source, name in ((observations, "observations.jsonl"), (selection, "selection.jsonl"), (review_path, "review.json")):
        shutil.copyfile(source, temporary / name)
    files = [
        {"path": name, "sha256": sha256_file(temporary / name), "bytes": (temporary / name).stat().st_size}
        for name in ("observations.jsonl", "selection.jsonl", "review.json")
    ]
    manifest = _manifest_with_hash({
        "schema_version": PRICE_RELEASE_SCHEMA_VERSION,
        "release_id": release_id,
        "previous_release_id": review.get("base_release_id"),
        "run_id": run_id,
        "snapshot_date": review["snapshot_date"],
        "created_at": _rfc3339(),
        "policy": policy,
        "input_sha256": input_sha,
        "observations_sha256": review["observations_sha256"],
        "selection_sha256": review["selection_sha256"],
        "review_sha256": sha256_file(review_path),
        "files": files,
        "stats": {
            "selected": len(_load_jsonl(selection)),
            "qualified_skus": review["qualified_skus"],
            "changed_skus": len(review["changed_skus"]),
            "quarantined": len(review["quarantined"]),
        },
    })
    _atomic_json(temporary / "manifest.json", manifest)
    os.replace(temporary, final_dir)
    return final_dir, manifest


def publish_price_review(paths: DataPaths, run_id: str, *, policy: str) -> dict[str, Any]:
    if policy not in {"manual", "automatic"}:
        raise PipelineError("invalid_price_policy", "价格发布 policy 仅允许 manual|automatic")
    _ensure(paths)
    with RunLock(paths.scheduler / "price-pipeline.lock"):
        review_path = _price_runs(paths) / run_id / "review.json"
        try:
            review = json.loads(review_path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as exc:
            raise PipelineError("price_review_missing", "价格 review 不存在或已损坏") from exc
        if review.get("decision") == "quarantine":
            raise PipelineError("price_quarantined", "价格批次达到系统性异常阈值")
        if review.get("decision") == "no_change":
            return {"schema_version": 1, "status": "no_change", "run_id": run_id}
        release_dir, manifest = _stage_price_release(paths, run_id, review, policy)
        result = subprocess.run(
            ["go", "run", "./cmd/importpriceobservations", "-release", str(release_dir)],
            cwd=paths.repo_root,
            check=False,
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            timeout=300,
        )
        if result.returncode != 0:
            raise PipelineError("price_database_import_failed", f"价格导入器失败，退出码 {result.returncode}")
        _atomic_json(_current_price(paths), {
            "schema_version": 1,
            "release_id": manifest["release_id"],
            "manifest_sha256": manifest["manifest_sha256"],
            "snapshot_date": manifest["snapshot_date"],
            "updated_at": _rfc3339(),
        })
        return {"schema_version": 1, "status": "published", "run_id": run_id, "release_id": manifest["release_id"]}


def price_health(paths: DataPaths) -> dict[str, Any]:
    _ensure(paths)
    manifest, selection = _read_current(paths)
    if manifest is None:
        return {"schema_version": 1, "healthy": False, "status": "missing", "model_used": False}
    today = _now().date()
    ages = [(today - datetime.fromisoformat(row["observed_at"].replace("Z", "+00:00")).date()).days for row in selection]
    return {
        "schema_version": 1,
        "healthy": bool(selection),
        "status": "stale" if ages and max(ages) > 14 else "ok",
        "release_id": manifest["release_id"],
        "snapshot_date": manifest["snapshot_date"],
        "selected": len(selection),
        "max_age_days": max(ages) if ages else None,
        "mean_age_days": round(sum(ages) / len(ages), 2) if ages else None,
        "model_used": False,
    }


def process_price_inbox(paths: DataPaths) -> dict[str, Any]:
    """按文件名串行处理本机 inbox；失败留档且不影响 P11 规格发布。"""
    _ensure(paths)
    files = sorted(_price_inbox(paths).glob("*.csv"))
    results: list[dict[str, Any]] = []
    for csv_path in files:
        run_id = str(uuid.uuid5(uuid.NAMESPACE_URL, f"pcdata-price-inbox:{sha256_file(csv_path)}"))
        try:
            import_price_csv(paths, csv_path, run_id=run_id)
            review = create_price_review(paths, run_id)
            result = publish_price_review(paths, run_id, policy="manual")
            results.append({"file": csv_path.name, "status": result["status"], "run_id": run_id})
            csv_path.replace(csv_path.with_suffix(".processed"))
        except (PipelineError, OSError, json.JSONDecodeError) as exc:
            results.append({"file": csv_path.name, "status": "partial", "error_code": getattr(exc, "code", "invalid_data")})
    return {
        "schema_version": 1,
        "status": "partial" if any(item["status"] == "partial" for item in results) else (
            "published" if any(item["status"] == "published" for item in results) else "no_change"
        ),
        "files": results,
        "model_used": False,
    }
