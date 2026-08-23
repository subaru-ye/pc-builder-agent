"""P11 本机数据运行、采集、审核、发布与恢复基座。

本模块刻意只依赖标准库。常规定时主链没有模型入口；网络适配器只能保存
原始快照，只有确定性 normalized parts 才可能进入风险分类和发布。
"""

from __future__ import annotations

import hashlib
import importlib.metadata
import ipaddress
import json
import os
import random
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import urllib.robotparser
import uuid
from contextlib import AbstractContextManager
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Any, Callable, Iterable
from urllib.parse import urlsplit, urlunsplit

from .canonical import CATEGORIES, SpecError
from .coverage import ERROR_LEVEL_FIELDS, load_parts_jsonl
from .registry import DEFAULT_REGISTRY_PATH, load_registry
from .sources import get_source

__all__ = [
    "DataPaths",
    "PipelineError",
    "RunLockedError",
    "HTTPCollector",
    "stable_json_bytes",
    "sha256_file",
    "create_review",
    "bootstrap_release",
    "publish_reviewed_release",
    "run_scheduled",
    "health_report",
]

SCHEMA_VERSION = 1
MAX_HTTP_BYTES = 10 * 1024 * 1024
MAX_HTTP_ATTEMPTS = 2
LOGIN_MARKERS = (
    "captcha",
    "recaptcha",
    "verify you are human",
    "security verification",
    "访问验证",
    "安全验证",
    "请输入验证码",
    "登录后查看",
)
SECRET_KEY_MARKERS = ("api_key", "apikey", "authorization", "cookie", "password", "secret", "token")


class PipelineError(RuntimeError):
    """P11 可安全展示的管道错误。"""

    def __init__(self, code: str, message: str):
        super().__init__(message)
        self.code = code


class RunLockedError(PipelineError):
    def __init__(self, message: str):
        super().__init__("already_running", message)


def _now() -> datetime:
    return datetime.now(UTC)


def _rfc3339(value: datetime | None = None) -> str:
    return (value or _now()).isoformat(timespec="seconds").replace("+00:00", "Z")


def _assert_safe(value: Any, path: str = "$") -> None:
    if isinstance(value, dict):
        for key, child in value.items():
            lowered = str(key).lower()
            if any(marker in lowered for marker in SECRET_KEY_MARKERS):
                raise PipelineError("sensitive_manifest", f"manifest 禁止字段 {path}.{key}")
            _assert_safe(child, f"{path}.{key}")
    elif isinstance(value, list):
        for index, child in enumerate(value):
            _assert_safe(child, f"{path}[{index}]")
    elif isinstance(value, str):
        lowered = value.lower()
        if "authorization:" in lowered or "cookie:" in lowered or lowered.startswith("sk-"):
            raise PipelineError("sensitive_manifest", f"manifest 在 {path} 疑似包含凭据")


def stable_json_bytes(value: Any, *, safe: bool = True) -> bytes:
    """稳定 JSON：UTF-8 无 BOM、LF、键排序、末尾换行。"""
    if safe:
        _assert_safe(value)
    return (
        json.dumps(value, ensure_ascii=False, sort_keys=True, indent=2, separators=(",", ": "))
        + "\n"
    ).encode("utf-8")


def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def _is_canonical_uuid(value: Any) -> bool:
    if not isinstance(value, str):
        return False
    try:
        return str(uuid.UUID(value)) == value
    except ValueError:
        return False


def _atomic_write(path: Path, data: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temp_name = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        with os.fdopen(fd, "wb") as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temp_name, path)
    except BaseException:
        try:
            os.unlink(temp_name)
        except FileNotFoundError:
            pass
        raise


def _atomic_json(path: Path, value: Any) -> None:
    _atomic_write(path, stable_json_bytes(value))


def _safe_source_url(url: str) -> str:
    parsed = urlsplit(url)
    return urlunsplit((parsed.scheme, parsed.netloc, parsed.path, "", ""))


class _NoRedirectHandler(urllib.request.HTTPRedirectHandler):
    """定时采集拒绝 redirect，避免跳到未登记主机或内网地址。"""

    def redirect_request(self, req, fp, code, msg, headers, newurl):  # type: ignore[no-untyped-def]
        raise urllib.error.HTTPError(req.full_url, code, "redirect blocked", headers, fp)


_SAFE_HTTP_OPENER = urllib.request.build_opener(_NoRedirectHandler)


def _open_without_redirect(request: urllib.request.Request, *, timeout: float):
    return _SAFE_HTTP_OPENER.open(request, timeout=timeout)


def _validate_http_endpoint(url: str, *, allow_http_for_test: bool) -> None:
    parsed = urlsplit(url)
    if parsed.scheme != "https" and not (allow_http_for_test and parsed.scheme == "http"):
        raise PipelineError("insecure_source", "自动来源必须使用 HTTPS")
    if not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment:
        raise PipelineError("invalid_source_url", "来源 URL 必须为不含凭据和查询参数的 HTTPS URL")
    if allow_http_for_test:
        return
    try:
        port = parsed.port or 443
        addresses = {
            ipaddress.ip_address(item[4][0])
            for item in socket.getaddrinfo(parsed.hostname, port, type=socket.SOCK_STREAM)
        }
    except (OSError, ValueError) as exc:
        raise PipelineError("source_dns_failed", f"来源 {parsed.hostname!r} 无法解析") from exc
    if not addresses or any(not address.is_global for address in addresses):
        raise PipelineError("ssrf_blocked", f"来源 {parsed.hostname!r} 解析到非公网地址")


def _validate_response_url(request_url: str, response: Any, *, allow_http_for_test: bool) -> None:
    geturl = getattr(response, "geturl", None)
    if not callable(geturl):
        return
    response_url = geturl()
    if response_url and response_url != request_url:
        raise PipelineError("redirect_blocked", "来源响应发生未批准的 redirect")
    if response_url:
        _validate_http_endpoint(response_url, allow_http_for_test=allow_http_for_test)


@dataclass(frozen=True)
class DataPaths:
    """仓库 seed 与本机运行状态的明确边界。"""

    repo_root: Path
    data_root: Path
    runtime_root: Path

    @classmethod
    def defaults(cls) -> "DataPaths":
        data_root = Path(__file__).resolve().parents[2]
        repo_root = data_root.parents[1]
        runtime = repo_root / "var" / "data"
        return cls(repo_root=repo_root, data_root=data_root, runtime_root=runtime)

    @property
    def seed_parts(self) -> Path:
        return self.data_root / "parts"

    @property
    def seed_prices(self) -> Path:
        return self.data_root / "prices"

    @property
    def runs(self) -> Path:
        return self.data_root / "work" / "runs"

    @property
    def quarantine(self) -> Path:
        return self.data_root / "work" / "quarantine"

    @property
    def releases(self) -> Path:
        return self.runtime_root / "releases"

    @property
    def checkpoints(self) -> Path:
        return self.runtime_root / "checkpoints"

    @property
    def scheduler(self) -> Path:
        return self.runtime_root / "scheduler"

    @property
    def current(self) -> Path:
        return self.runtime_root / "current.json"

    @property
    def pending_activation(self) -> Path:
        return self.scheduler / "pending-activation.json"

    def ensure(self) -> None:
        for path in (self.runs, self.quarantine, self.releases, self.checkpoints, self.scheduler):
            path.mkdir(parents=True, exist_ok=True)


class RunLock(AbstractContextManager["RunLock"]):
    """由操作系统持有的跨进程锁；进程退出后自动释放，不怕残留文件。"""

    def __init__(self, path: Path):
        self.path = path
        self._held = False
        self._stream: Any = None

    def __enter__(self) -> "RunLock":
        self.path.parent.mkdir(parents=True, exist_ok=True)
        payload = stable_json_bytes(
            {
                "schema_version": SCHEMA_VERSION,
                "pid": os.getpid(),
                "host": socket.gethostname(),
                "started_at": _rfc3339(),
            }
        )
        self._stream = self.path.open("a+b")
        if self.path.stat().st_size == 0:
            self._stream.write(b"\0")
            self._stream.flush()
        self._stream.seek(0)
        try:
            if os.name == "nt":
                import msvcrt

                msvcrt.locking(self._stream.fileno(), msvcrt.LK_NBLCK, 1)
            else:
                import fcntl

                fcntl.flock(self._stream.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        except OSError as exc:
            self._stream.close()
            self._stream = None
            raise RunLockedError(f"已有数据任务持有锁: {self.path}") from exc
        self._stream.seek(0)
        self._stream.truncate()
        self._stream.write(payload)
        self._stream.flush()
        self._held = True
        return self

    def __exit__(self, exc_type, exc_value, traceback) -> None:
        if self._held:
            try:
                self._stream.seek(0)
                if os.name == "nt":
                    import msvcrt

                    msvcrt.locking(self._stream.fileno(), msvcrt.LK_UNLCK, 1)
                else:
                    import fcntl

                    fcntl.flock(self._stream.fileno(), fcntl.LOCK_UN)
            finally:
                self._stream.close()
                self._stream = None
            self._held = False


class CheckpointStore:
    def __init__(self, root: Path):
        self.root = root

    def _path(self, source_id: str) -> Path:
        if not source_id.replace("_", "").isalnum():
            raise PipelineError("invalid_source", "source_id 不能用于检查点路径")
        return self.root / f"{source_id}.json"

    def load(self, source_id: str) -> dict[str, Any]:
        path = self._path(source_id)
        if not path.exists():
            return {"schema_version": SCHEMA_VERSION, "source_id": source_id}
        try:
            value = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as exc:
            raise PipelineError("checkpoint_invalid", f"检查点损坏: {path}") from exc
        if not isinstance(value, dict) or value.get("source_id") != source_id:
            raise PipelineError("checkpoint_invalid", f"检查点来源不匹配: {path}")
        return value

    def save(self, source_id: str, value: dict[str, Any]) -> None:
        out = dict(value)
        out["schema_version"] = SCHEMA_VERSION
        out["source_id"] = source_id
        _atomic_json(self._path(source_id), out)


class HTTPCollector:
    """保存低频 HTTP 原始快照，不解析业务字段。"""

    def __init__(
        self,
        *,
        timeout_seconds: float = 30,
        max_bytes: int = MAX_HTTP_BYTES,
        opener: Callable[..., Any] = urllib.request.urlopen,
        allow_http_for_test: bool = False,
    ):
        self.timeout_seconds = timeout_seconds
        self.max_bytes = max_bytes
        self.opener = _open_without_redirect if opener is urllib.request.urlopen else opener
        self.allow_http_for_test = allow_http_for_test

    @staticmethod
    def _record_failed_attempt(
        source_id: str,
        checkpoints: CheckpointStore,
        previous: dict[str, Any],
        attempted_at: str,
        error_code: str,
    ) -> None:
        checkpoint = {
            key: previous[key]
            for key in ("etag", "last_modified", "content_sha256")
            if previous.get(key) is not None
        }
        checkpoint.update({"last_attempt_at": attempted_at, "status": error_code})
        try:
            checkpoints.save(source_id, checkpoint)
        except (OSError, PipelineError):
            # 原始采集错误优先返回；检查点写失败会在下一次 health/run 中暴露。
            pass

    def _open_with_retry(self, request: urllib.request.Request):
        for attempt in range(MAX_HTTP_ATTEMPTS):
            try:
                return self.opener(request, timeout=self.timeout_seconds)
            except urllib.error.HTTPError as exc:
                if exc.code not in {408, 500, 502, 503, 504} or attempt + 1 >= MAX_HTTP_ATTEMPTS:
                    raise
            except (TimeoutError, urllib.error.URLError):
                if attempt + 1 >= MAX_HTTP_ATTEMPTS:
                    raise
            time.sleep(0.1 * (2**attempt) + random.uniform(0, 0.05))
        raise PipelineError("network_error", "HTTP 请求重试失败")

    def _check_robots(self, source: dict[str, Any], user_agent: str) -> None:
        parsed = urlsplit(source["base_url"])
        robots_url = urlunsplit((parsed.scheme, parsed.netloc, "/robots.txt", "", ""))
        _validate_http_endpoint(robots_url, allow_http_for_test=self.allow_http_for_test)
        request = urllib.request.Request(robots_url, headers={"User-Agent": user_agent}, method="GET")
        try:
            response = self._open_with_retry(request)
            with response:
                status = int(getattr(response, "status", response.getcode()))
                data = response.read(min(self.max_bytes, 512 * 1024) + 1)
                _validate_response_url(robots_url, response, allow_http_for_test=self.allow_http_for_test)
        except urllib.error.HTTPError as exc:
            if exc.code == 404:
                return
            if 300 <= exc.code < 400:
                raise PipelineError("redirect_blocked", f"来源 {source['id']} robots.txt 禁止 redirect") from exc
            code = "rate_limited" if exc.code == 429 else "access_denied" if exc.code in {401, 403} else "robots_unavailable"
            raise PipelineError(code, f"来源 {source['id']} robots.txt HTTP {exc.code}") from exc
        except (TimeoutError, urllib.error.URLError) as exc:
            raise PipelineError("robots_unavailable", f"来源 {source['id']} 无法检查 robots.txt") from exc
        if status != 200 or len(data) > min(self.max_bytes, 512 * 1024):
            raise PipelineError("robots_unavailable", f"来源 {source['id']} robots.txt 响应异常")
        parser = urllib.robotparser.RobotFileParser()
        parser.set_url(robots_url)
        parser.parse(data.decode("utf-8", errors="replace").splitlines())
        if not parser.can_fetch(user_agent, source["base_url"]):
            raise PipelineError("robots_disallowed", f"来源 {source['id']} robots.txt 禁止自动访问")

    def collect(
        self,
        source: dict[str, Any],
        raw_dir: Path,
        checkpoints: CheckpointStore,
    ) -> dict[str, Any]:
        if source["adapter"] != "http_snapshot":
            raise PipelineError("adapter_mismatch", f"{source['id']} 不是 http_snapshot")
        if not source["enabled"]:
            raise PipelineError("source_disabled", f"来源 {source['id']} 未启用")
        if source["automated_access"] not in {"allowed", "allowed_after_review"}:
            raise PipelineError("source_not_allowed", f"来源 {source['id']} 未允许自动访问")
        if source.get("requires_credentials"):
            raise PipelineError("credentials_required", f"来源 {source['id']} 需要凭据，定时采集不允许启用")
        url = source["base_url"]
        _validate_http_endpoint(url, allow_http_for_test=self.allow_http_for_test)

        previous = checkpoints.load(source["id"])
        attempted_at = _rfc3339()
        user_agent = "pc-builder-agent-data/1.0 (+local-learning-project)"
        try:
            self._check_robots(source, user_agent)
        except PipelineError as exc:
            self._record_failed_attempt(source["id"], checkpoints, previous, attempted_at, exc.code)
            raise
        headers = {
            "Accept": "text/html,application/json;q=0.9,*/*;q=0.1",
            "User-Agent": user_agent,
        }
        if previous.get("etag"):
            headers["If-None-Match"] = previous["etag"]
        if previous.get("last_modified"):
            headers["If-Modified-Since"] = previous["last_modified"]
        request = urllib.request.Request(url, headers=headers, method="GET")
        try:
            response = self._open_with_retry(request)
            with response:
                status = int(getattr(response, "status", response.getcode()))
                content_type = response.headers.get("Content-Type", "").split(";", 1)[0].strip().lower()
                data = response.read(self.max_bytes + 1)
                response_headers = response.headers
                _validate_response_url(url, response, allow_http_for_test=self.allow_http_for_test)
        except urllib.error.HTTPError as exc:
            if exc.code == 304:
                checkpoint = dict(previous)
                checkpoint.update({"last_attempt_at": attempted_at, "last_success_at": attempted_at, "status": "not_modified"})
                checkpoints.save(source["id"], checkpoint)
                return {
                    "source_id": source["id"],
                    "status": "not_modified",
                    "http_status": 304,
                    "captured_at": attempted_at,
                }
            if 300 <= exc.code < 400:
                self._record_failed_attempt(source["id"], checkpoints, previous, attempted_at, "redirect_blocked")
                raise PipelineError("redirect_blocked", f"来源 {source['id']} 禁止 redirect") from exc
            code = "rate_limited" if exc.code == 429 else "access_denied" if exc.code in {401, 403} else "http_error"
            self._record_failed_attempt(source["id"], checkpoints, previous, attempted_at, code)
            raise PipelineError(code, f"来源 {source['id']} HTTP {exc.code}") from exc
        except TimeoutError as exc:
            self._record_failed_attempt(source["id"], checkpoints, previous, attempted_at, "timeout")
            raise PipelineError("timeout", f"来源 {source['id']} 请求超时") from exc
        except urllib.error.URLError as exc:
            self._record_failed_attempt(source["id"], checkpoints, previous, attempted_at, "network_error")
            raise PipelineError("network_error", f"来源 {source['id']} 网络失败") from exc
        except PipelineError as exc:
            self._record_failed_attempt(source["id"], checkpoints, previous, attempted_at, exc.code)
            raise

        if status != 200:
            self._record_failed_attempt(source["id"], checkpoints, previous, attempted_at, "http_error")
            raise PipelineError("http_error", f"来源 {source['id']} HTTP {status}")
        if len(data) > self.max_bytes:
            self._record_failed_attempt(source["id"], checkpoints, previous, attempted_at, "response_too_large")
            raise PipelineError("response_too_large", f"来源 {source['id']} 响应超过 {self.max_bytes} 字节")
        if not data:
            self._record_failed_attempt(source["id"], checkpoints, previous, attempted_at, "empty_response")
            raise PipelineError("empty_response", f"来源 {source['id']} 返回空内容")
        sample = data[:200_000].decode("utf-8", errors="ignore").lower()
        if any(marker in sample for marker in LOGIN_MARKERS):
            self._record_failed_attempt(source["id"], checkpoints, previous, attempted_at, "access_challenge")
            raise PipelineError("access_challenge", f"来源 {source['id']} 返回登录或验证页面")
        if content_type and not (
            content_type.startswith("text/")
            or content_type in {"application/json", "application/pdf", "application/octet-stream"}
        ):
            self._record_failed_attempt(source["id"], checkpoints, previous, attempted_at, "unexpected_content_type")
            raise PipelineError("unexpected_content_type", f"来源 {source['id']} Content-Type={content_type}")

        digest = sha256_bytes(data)
        raw_dir.mkdir(parents=True, exist_ok=True)
        suffix = ".json" if content_type == "application/json" else ".pdf" if content_type == "application/pdf" else ".bin"
        raw_path = raw_dir / f"{source['id']}-{digest[:16]}{suffix}"
        if not raw_path.exists():
            _atomic_write(raw_path, data)
        unchanged = previous.get("content_sha256") == digest
        checkpoint = {
            "etag": response_headers.get("ETag"),
            "last_modified": response_headers.get("Last-Modified"),
            "content_sha256": digest,
            "last_attempt_at": attempted_at,
            "last_success_at": attempted_at,
            "status": "not_modified" if unchanged else "collected",
        }
        checkpoints.save(source["id"], checkpoint)
        return {
            "source_id": source["id"],
            "status": checkpoint["status"],
            "http_status": 200,
            "content_type": content_type or None,
            "content_sha256": digest,
            "bytes": len(data),
            "captured_at": attempted_at,
            "source_url": _safe_source_url(url),
            "raw_file": raw_path.name,
        }


def _category_files(parts_dir: Path) -> list[Path]:
    expected = [parts_dir / f"{category}.jsonl" for category in CATEGORIES]
    missing = [path.name for path in expected if not path.is_file()]
    extras = sorted(path.name for path in parts_dir.glob("*.jsonl") if path not in expected)
    if missing or extras:
        raise PipelineError(
            "parts_layout_invalid",
            f"parts 目录不完整: missing={missing}, extra={extras}",
        )
    return expected


def _load_parts(parts_dir: Path) -> dict[str, dict[str, Any]]:
    records: dict[str, dict[str, Any]] = {}
    for path in _category_files(parts_dir):
        for record in load_parts_jsonl(path):
            sku = record["sku"]
            if sku in records:
                raise PipelineError("duplicate_sku", f"跨文件重复 SKU {sku}")
            _assert_safe(record["source_meta"], f"parts[{sku}].source_meta")
            records[sku] = record
    return records


def _parts_digest(parts_dir: Path) -> str:
    digest = hashlib.sha256()
    for path in _category_files(parts_dir):
        digest.update(path.name.encode("utf-8"))
        digest.update(b"\0")
        digest.update(path.read_bytes())
        digest.update(b"\0")
    return digest.hexdigest()


def _copy_parts(source: Path, target: Path) -> None:
    target.mkdir(parents=True, exist_ok=True)
    for path in _category_files(source):
        data = path.read_bytes()
        if data.startswith(b"\xef\xbb\xbf") or not data.endswith(b"\n") or b"\r" in data:
            raise PipelineError("serialization_invalid", f"{path} 必须为 UTF-8 无 BOM、LF、末尾换行")
        _atomic_write(target / path.name, data)


def _read_current(paths: DataPaths) -> dict[str, Any] | None:
    if not paths.current.exists():
        return None
    try:
        value = json.loads(paths.current.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise PipelineError("current_invalid", "current.json 损坏") from exc
    if not isinstance(value, dict) or set(value) != {"schema_version", "release_id", "manifest_sha256", "updated_at"}:
        raise PipelineError("current_invalid", "current.json 结构非法")
    if value.get("schema_version") != SCHEMA_VERSION:
        raise PipelineError("current_invalid", "current.json schema_version 非法")
    release_id = value.get("release_id")
    if not _is_canonical_uuid(release_id):
        raise PipelineError("current_invalid", "current.json release_id 非法")
    if (
        not isinstance(value.get("manifest_sha256"), str)
        or len(value["manifest_sha256"]) != 64
        or any(char not in "0123456789abcdef" for char in value["manifest_sha256"])
    ):
        raise PipelineError("current_invalid", "current.json manifest_sha256 非法")
    if not (paths.releases / release_id / "manifest.json").is_file():
        raise PipelineError("current_invalid", "current.json 指向不存在的 release")
    manifest = _verify_release_dir(paths.releases / release_id)
    if value.get("manifest_sha256") != manifest["manifest_sha256"]:
        raise PipelineError("current_invalid", "current.json 与 release manifest 哈希不一致")
    return value


def _current_parts(paths: DataPaths) -> tuple[str | None, Path | None]:
    current = _read_current(paths)
    if current is None:
        return None, None
    release_id = current["release_id"]
    return release_id, paths.releases / release_id / "parts"


def _field_changes(before: dict[str, Any], after: dict[str, Any]) -> list[dict[str, Any]]:
    changes: list[dict[str, Any]] = []
    for field in ("category", "brand", "model", "schema_version"):
        if before[field] != after[field]:
            changes.append({"field": field, "before": before[field], "after": after[field]})
    keys = sorted(set(before["specs"]) | set(after["specs"]))
    for field in keys:
        old, new = before["specs"].get(field), after["specs"].get(field)
        if old != new:
            changes.append({"field": f"specs.{field}", "before": old, "after": new})
    if before["source_meta"] != after["source_meta"]:
        changes.append({"field": "source_meta", "before": before["source_meta"], "after": after["source_meta"]})
    return changes


def create_review(
    *,
    run_id: str,
    candidate_parts: Path,
    base_parts: Path | None,
    base_release_id: str | None,
    model_used: bool = False,
) -> dict[str, Any]:
    """比较 candidate 与 last-known-good，并作确定性风险分类。"""
    candidate = _load_parts(candidate_parts)
    base = _load_parts(base_parts) if base_parts is not None else {}
    changes: list[dict[str, Any]] = []
    risks: list[dict[str, Any]] = []

    for sku in sorted(set(base) | set(candidate)):
        if sku not in base:
            changes.append({"sku": sku, "kind": "added", "fields": []})
            if base_parts is not None:
                risks.append({"code": "new_sku_requires_evidence", "sku": sku, "severity": "high"})
            continue
        if sku not in candidate:
            changes.append({"sku": sku, "kind": "removed", "fields": []})
            risks.append({"code": "implicit_removal", "sku": sku, "severity": "critical"})
            continue
        fields = _field_changes(base[sku], candidate[sku])
        if not fields:
            continue
        changes.append({"sku": sku, "kind": "modified", "fields": fields})
        category = candidate[sku]["category"]
        critical = set(ERROR_LEVEL_FIELDS.get(category, ()))
        for change in fields:
            field = change["field"]
            if field in {"category", "brand", "model"} or (
                field.startswith("specs.") and field.removeprefix("specs.") in critical
            ):
                risks.append(
                    {
                        "code": "critical_identity_or_spec_changed",
                        "sku": sku,
                        "field": field,
                        "severity": "critical",
                    }
                )
    if model_used:
        risks.append({"code": "model_output_requires_manual_review", "severity": "high"})

    if base_parts is None:
        decision = "manual_required"
    elif not changes:
        decision = "no_change"
    elif risks:
        decision = "quarantine"
    else:
        decision = "auto_publish"
    return {
        "schema_version": SCHEMA_VERSION,
        "run_id": run_id,
        "created_at": _rfc3339(),
        "base_release_id": base_release_id,
        "input_sha256": _parts_digest(candidate_parts),
        "changes": changes,
        "risks": risks,
        "decision": decision,
    }


def _review_markdown(review: dict[str, Any]) -> str:
    lines = [
        f"# 数据审核 {review['run_id']}",
        "",
        f"- 决定：`{review['decision']}`",
        f"- 基线：`{review['base_release_id'] or 'none'}`",
        f"- 输入 SHA-256：`{review['input_sha256']}`",
        f"- 变化：{len(review['changes'])}",
        f"- 风险：{len(review['risks'])}",
        "",
        "## 变化",
        "",
    ]
    if not review["changes"]:
        lines.append("无语义变化。")
    for change in review["changes"]:
        lines.append(f"- `{change['sku']}`：{change['kind']}")
        for field in change.get("fields", []):
            lines.append(f"  - `{field['field']}`：`{field['before']}` → `{field['after']}`")
    lines.extend(["", "## 风险", ""])
    if not review["risks"]:
        lines.append("无阻断风险。")
    for risk in review["risks"]:
        suffix = f"，SKU `{risk['sku']}`" if risk.get("sku") else ""
        field = f"，字段 `{risk['field']}`" if risk.get("field") else ""
        lines.append(f"- `{risk['severity']}` `{risk['code']}`{suffix}{field}")
    return "\n".join(lines) + "\n"


def _write_review(run_dir: Path, review: dict[str, Any]) -> None:
    _atomic_json(run_dir / "review.json", review)
    _atomic_write(run_dir / "review.md", _review_markdown(review).encode("utf-8"))


def _validate_review(review: Any, *, run_id: str) -> None:
    if not isinstance(review, dict):
        raise PipelineError("review_invalid", "review 必须为对象")
    required = {
        "schema_version",
        "run_id",
        "created_at",
        "base_release_id",
        "input_sha256",
        "changes",
        "risks",
        "decision",
    }
    if set(review) != required or review.get("schema_version") != SCHEMA_VERSION:
        raise PipelineError("review_invalid", "review 结构或 schema_version 非法")
    if review.get("run_id") != run_id or not _is_canonical_uuid(run_id):
        raise PipelineError("review_invalid", "review run_id 不匹配")
    base_release_id = review.get("base_release_id")
    if base_release_id is not None and not _is_canonical_uuid(base_release_id):
        raise PipelineError("review_invalid", "review base_release_id 非法")
    input_sha256 = review.get("input_sha256")
    if (
        not isinstance(input_sha256, str)
        or len(input_sha256) != 64
        or any(char not in "0123456789abcdef" for char in input_sha256)
    ):
        raise PipelineError("review_invalid", "review input_sha256 非法")
    if not isinstance(review.get("changes"), list) or not isinstance(review.get("risks"), list):
        raise PipelineError("review_invalid", "review changes/risks 必须为数组")
    if review.get("decision") not in {"no_change", "auto_publish", "quarantine", "manual_required"}:
        raise PipelineError("review_invalid", "review decision 非法")
    _assert_safe(review)


def _manifest_with_hash(manifest: dict[str, Any]) -> dict[str, Any]:
    value = dict(manifest)
    value["manifest_sha256"] = ""
    digest = sha256_bytes(stable_json_bytes(value))
    value["manifest_sha256"] = digest
    return value


def _verify_release_dir(
    release_dir: Path,
    *,
    expected_run_id: str | None = None,
    expected_input_sha256: str | None = None,
    expected_decision: str | None = None,
    expected_previous_release_id: str | None = None,
) -> dict[str, Any]:
    try:
        manifest = json.loads((release_dir / "manifest.json").read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise PipelineError("release_invalid", f"release manifest 损坏: {release_dir.name}") from exc
    if not isinstance(manifest, dict):
        raise PipelineError("release_invalid", "release manifest 必须为对象")
    _assert_safe(manifest)
    required = {
        "schema_version",
        "release_id",
        "previous_release_id",
        "run_id",
        "created_at",
        "decision",
        "input_sha256",
        "files",
        "stats",
        "manifest_sha256",
    }
    if set(manifest) != required:
        raise PipelineError("release_invalid", "release manifest 字段集合非法")
    if manifest.get("schema_version") != SCHEMA_VERSION:
        raise PipelineError("release_invalid", "release schema_version 非法")
    release_id = manifest.get("release_id")
    if not _is_canonical_uuid(release_id):
        raise PipelineError("release_invalid", "release_id 非法")
    if manifest.get("run_id") is not None:
        run_id = manifest.get("run_id")
        if not _is_canonical_uuid(run_id):
            raise PipelineError("release_invalid", "run_id 非法")
    previous = manifest.get("previous_release_id")
    if previous is not None and not _is_canonical_uuid(previous):
        raise PipelineError("release_invalid", "previous_release_id 非法")
    if manifest.get("decision") not in {"auto", "manual", "bootstrap"}:
        raise PipelineError("release_invalid", "release decision 非法")
    if not isinstance(manifest.get("created_at"), str):
        raise PipelineError("release_invalid", "created_at 非法")
    try:
        datetime.fromisoformat(manifest["created_at"].replace("Z", "+00:00"))
    except ValueError as exc:
        raise PipelineError("release_invalid", "created_at 非法") from exc
    if not isinstance(manifest.get("stats"), dict):
        raise PipelineError("release_invalid", "release stats 必须为对象")
    if (
        not isinstance(manifest.get("input_sha256"), str)
        or len(manifest["input_sha256"]) != 64
        or any(char not in "0123456789abcdef" for char in manifest["input_sha256"])
    ):
        raise PipelineError("release_invalid", "release input_sha256 非法")
    claimed = manifest.get("manifest_sha256")
    canonical = dict(manifest)
    canonical["manifest_sha256"] = ""
    if not isinstance(claimed, str) or sha256_bytes(stable_json_bytes(canonical)) != claimed:
        raise PipelineError("release_invalid", "release manifest_sha256 不匹配")
    if manifest.get("release_id") != release_dir.name:
        raise PipelineError("release_invalid", "release_id 与目录名不一致")
    if expected_run_id is not None and manifest.get("run_id") != expected_run_id:
        raise PipelineError("release_collision", "既有 release 的 run_id 不一致")
    if expected_input_sha256 is not None and manifest.get("input_sha256") != expected_input_sha256:
        raise PipelineError("release_collision", "既有 release 的输入哈希不一致")
    if expected_decision is not None and manifest.get("decision") != expected_decision:
        raise PipelineError("release_collision", "既有 release 的发布决定不一致")
    if expected_previous_release_id is not None and manifest.get("previous_release_id") != expected_previous_release_id:
        raise PipelineError("release_collision", "既有 release 的 last-known-good 基线不一致")
    files = manifest.get("files")
    if not isinstance(files, list) or len(files) != len(CATEGORIES):
        raise PipelineError("release_invalid", "release files 必须包含八类 parts")
    expected_paths = {f"parts/{category}.jsonl" for category in CATEGORIES}
    actual_paths: set[str] = set()
    for entry in files:
        if not isinstance(entry, dict) or set(entry) != {"path", "sha256", "bytes"}:
            raise PipelineError("release_invalid", "release file 条目结构非法")
        relative = entry["path"]
        if (
            not isinstance(relative, str)
            or relative not in expected_paths
            or not isinstance(entry["sha256"], str)
            or len(entry["sha256"]) != 64
            or not isinstance(entry["bytes"], int)
            or isinstance(entry["bytes"], bool)
            or entry["bytes"] < 0
        ):
            raise PipelineError("release_invalid", f"release file 路径非法: {relative!r}")
        actual_paths.add(relative)
        path = release_dir / relative
        if not path.is_file() or path.stat().st_size != entry["bytes"] or sha256_file(path) != entry["sha256"]:
            raise PipelineError("release_invalid", f"release file 哈希不匹配: {relative}")
    if actual_paths != expected_paths:
        raise PipelineError("release_invalid", "release files 缺少或重复类目")
    if _parts_digest(release_dir / "parts") != manifest.get("input_sha256"):
        raise PipelineError("release_invalid", "release parts 与 input_sha256 不一致")
    return manifest


def _write_pending_activation(paths: DataPaths, manifest: dict[str, Any]) -> None:
    _atomic_json(
        paths.pending_activation,
        {
            "schema_version": SCHEMA_VERSION,
            "release_id": manifest["release_id"],
            "previous_release_id": manifest.get("previous_release_id"),
        },
    )


def _clear_pending_activation(paths: DataPaths) -> None:
    try:
        paths.pending_activation.unlink()
    except FileNotFoundError:
        pass


def _rollback_database_projection(
    paths: DataPaths,
    previous_release_id: str | None,
    *,
    importer: Importer | None,
) -> None:
    if previous_release_id is None:
        return
    previous_dir = paths.releases / previous_release_id
    _verify_release_dir(previous_dir)
    (importer or (lambda _repo, release: _default_importer(paths.repo_root, release)))(
        paths.repo_root, previous_dir
    )


def _recover_pending_activation(
    paths: DataPaths,
    *,
    importer: Importer | None,
) -> dict[str, Any] | None:
    if not paths.pending_activation.exists():
        return None
    try:
        pending = json.loads(paths.pending_activation.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise PipelineError("pending_activation_invalid", "待恢复 activation journal 损坏") from exc
    if (
        not isinstance(pending, dict)
        or set(pending) != {"schema_version", "release_id", "previous_release_id"}
        or pending.get("schema_version") != SCHEMA_VERSION
        or not _is_canonical_uuid(pending.get("release_id"))
        or (
            pending.get("previous_release_id") is not None
            and not _is_canonical_uuid(pending.get("previous_release_id"))
        )
    ):
        raise PipelineError("pending_activation_invalid", "待恢复 activation journal 结构非法")
    release_dir = paths.releases / pending["release_id"]
    manifest = _verify_release_dir(release_dir)
    if manifest.get("previous_release_id") != pending.get("previous_release_id"):
        raise PipelineError("pending_activation_invalid", "待恢复 release 基线不一致")
    current = _read_current(paths)
    if current is not None and current["release_id"] not in {
        pending["release_id"],
        pending.get("previous_release_id"),
    }:
        raise PipelineError("pending_activation_conflict", "待恢复 release 与 current.json 冲突")
    if current is not None and current["release_id"] == pending["release_id"]:
        _clear_pending_activation(paths)
        return manifest
    (importer or (lambda _repo, release: _default_importer(paths.repo_root, release)))(
        paths.repo_root, release_dir
    )
    try:
        _activate_release(paths, manifest)
    except OSError as exc:
        raise PipelineError("current_switch_failed", "恢复数据库发布后切换 current.json 失败") from exc
    _clear_pending_activation(paths)
    return manifest


Importer = Callable[[Path, Path], None]


def _default_importer(repo_root: Path, release_dir: Path) -> None:
    command = [
        "go",
        "run",
        "./cmd/importparts",
        "-dir",
        str(release_dir / "parts"),
        "-release-manifest",
        str(release_dir / "manifest.json"),
    ]
    result = subprocess.run(
        command,
        cwd=repo_root,
        check=False,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        timeout=300,
    )
    if result.returncode != 0:
        # 不回传 stdout/stderr，避免上游意外内容进入 manifest 或产品日志。
        raise PipelineError("database_import_failed", f"Go 导入器失败，退出码 {result.returncode}")


def sync_run_to_database(repo_root: Path, manifest_path: Path) -> None:
    """调用固定 Go 命令同步 run；不接受环境中的任意命令字符串。"""
    result = subprocess.run(
        ["go", "run", "./cmd/datajob", "-manifest", str(manifest_path)],
        cwd=repo_root,
        check=False,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        timeout=120,
    )
    if result.returncode != 0:
        raise PipelineError("job_record_failed", f"run 数据库同步失败，退出码 {result.returncode}")


def _stage_release(
    *,
    paths: DataPaths,
    run_id: str,
    candidate_parts: Path,
    previous_release_id: str | None,
    decision: str,
    review: dict[str, Any],
) -> tuple[Path, dict[str, Any]]:
    release_id = str(uuid.uuid5(uuid.NAMESPACE_URL, f"pcdata:{run_id}:{review['input_sha256']}:{decision}"))
    final_dir = paths.releases / release_id
    if final_dir.exists():
        manifest = _verify_release_dir(
            final_dir,
            expected_run_id=run_id,
            expected_input_sha256=review["input_sha256"],
            expected_decision=decision,
            expected_previous_release_id=previous_release_id,
        )
        return final_dir, manifest
    temp_dir = paths.releases / f".{release_id}.tmp"
    if temp_dir.exists():
        shutil.rmtree(temp_dir)
    temp_dir.mkdir(parents=True)
    _copy_parts(candidate_parts, temp_dir / "parts")
    files = [
        {"path": f"parts/{path.name}", "sha256": sha256_file(path), "bytes": path.stat().st_size}
        for path in _category_files(temp_dir / "parts")
    ]
    manifest = _manifest_with_hash(
        {
            "schema_version": SCHEMA_VERSION,
            "release_id": release_id,
            "previous_release_id": previous_release_id,
            "run_id": run_id,
            "created_at": _rfc3339(),
            "decision": decision,
            "input_sha256": review["input_sha256"],
            "files": files,
            "stats": {
                "total_skus": len(_load_parts(candidate_parts)),
                "added": sum(1 for c in review["changes"] if c["kind"] == "added"),
                "modified": sum(1 for c in review["changes"] if c["kind"] == "modified"),
                "removed": sum(1 for c in review["changes"] if c["kind"] == "removed"),
            },
        }
    )
    _atomic_json(temp_dir / "manifest.json", manifest)
    os.replace(temp_dir, final_dir)
    return final_dir, _verify_release_dir(
        final_dir,
        expected_run_id=run_id,
        expected_input_sha256=review["input_sha256"],
        expected_decision=decision,
        expected_previous_release_id=previous_release_id,
    )


def _activate_release(paths: DataPaths, manifest: dict[str, Any]) -> None:
    _atomic_json(
        paths.current,
        {
            "schema_version": SCHEMA_VERSION,
            "release_id": manifest["release_id"],
            "manifest_sha256": manifest["manifest_sha256"],
            "updated_at": _rfc3339(),
        },
    )


def _bootstrap_release_unlocked(
    paths: DataPaths,
    *,
    importer: Importer | None = None,
) -> dict[str, Any]:
    """把仓库 seed 显式建立为初始 last-known-good；重复执行幂等。"""
    paths.ensure()
    current = _read_current(paths)
    if current is not None:
        return current
    run_id = str(uuid.uuid5(uuid.NAMESPACE_URL, f"pcdata:bootstrap:{_parts_digest(paths.seed_parts)}"))
    run_dir = paths.runs / run_id
    candidate = run_dir / "normalized" / "parts"
    _copy_parts(paths.seed_parts, candidate)
    review = create_review(
        run_id=run_id,
        candidate_parts=candidate,
        base_parts=None,
        base_release_id=None,
    )
    _write_review(run_dir, review)
    release_dir, manifest = _stage_release(
        paths=paths,
        run_id=run_id,
        candidate_parts=candidate,
        previous_release_id=None,
        decision="bootstrap",
        review=review,
    )
    _write_pending_activation(paths, manifest)
    (importer or (lambda _repo, release: _default_importer(paths.repo_root, release)))(
        paths.repo_root, release_dir
    )
    try:
        _activate_release(paths, manifest)
    except OSError as exc:
        if manifest.get("previous_release_id") is not None:
            try:
                _rollback_database_projection(paths, manifest["previous_release_id"], importer=importer)
                _clear_pending_activation(paths)
            except (PipelineError, OSError) as rollback_exc:
                raise PipelineError("rollback_failed", "current.json 切换失败且数据库补偿回滚失败") from rollback_exc
        raise PipelineError("current_switch_failed", "数据库成功但切换 current.json 失败；下次运行将恢复") from exc
    _clear_pending_activation(paths)
    _atomic_json(
        run_dir / "manifest.json",
        {
            "schema_version": SCHEMA_VERSION,
            "run_id": run_id,
            "profile": "weekly",
            "trigger": "manual",
            "scheduled_for": _rfc3339(),
            "started_at": _rfc3339(),
            "finished_at": _rfc3339(),
            "status": "published",
            "model_used": False,
            "sources": [{"source_id": "seed_catalog", "status": "collected"}],
            "summary": {"release_id": manifest["release_id"], "decision": "bootstrap"},
            "error": None,
        },
    )
    return json.loads(paths.current.read_text(encoding="utf-8"))


def bootstrap_release(
    paths: DataPaths,
    *,
    importer: Importer | None = None,
) -> dict[str, Any]:
    paths.ensure()
    with RunLock(paths.scheduler / "pipeline.lock"):
        _recover_pending_activation(paths, importer=importer)
        return _bootstrap_release_unlocked(paths, importer=importer)


def _publish_reviewed_release_unlocked(
    paths: DataPaths,
    *,
    run_id: str,
    candidate_parts: Path,
    review: dict[str, Any],
    policy: str,
    importer: Importer | None = None,
) -> dict[str, Any]:
    _validate_review(review, run_id=run_id)
    if policy not in {"auto", "manual"}:
        raise PipelineError("invalid_policy", "policy 仅允许 auto|manual")
    if _parts_digest(candidate_parts) != review["input_sha256"]:
        raise PipelineError("review_drift", "审核后输入发生变化")
    if policy == "auto" and review["decision"] != "auto_publish":
        raise PipelineError("auto_publish_blocked", f"审核决定为 {review['decision']}")
    if policy == "manual" and review["decision"] == "no_change":
        raise PipelineError("no_change", "没有需要发布的变化")
    current = _read_current(paths)
    previous = current["release_id"] if current else None
    if review["base_release_id"] != previous:
        raise PipelineError("review_stale", "review 的 last-known-good 基线已过期")
    release_dir, manifest = _stage_release(
        paths=paths,
        run_id=run_id,
        candidate_parts=candidate_parts,
        previous_release_id=previous,
        decision=policy,
        review=review,
    )
    _write_pending_activation(paths, manifest)
    (importer or (lambda _repo, release: _default_importer(paths.repo_root, release)))(
        paths.repo_root, release_dir
    )
    try:
        _activate_release(paths, manifest)
    except OSError as exc:
        if manifest.get("previous_release_id") is not None:
            try:
                _rollback_database_projection(paths, manifest["previous_release_id"], importer=importer)
                _clear_pending_activation(paths)
            except (PipelineError, OSError) as rollback_exc:
                raise PipelineError("rollback_failed", "current.json 切换失败且数据库补偿回滚失败") from rollback_exc
        raise PipelineError("current_switch_failed", "数据库成功但切换 current.json 失败；下次运行将恢复") from exc
    _clear_pending_activation(paths)
    return manifest


def publish_reviewed_release(
    paths: DataPaths,
    *,
    run_id: str,
    candidate_parts: Path,
    review: dict[str, Any],
    policy: str,
    importer: Importer | None = None,
) -> dict[str, Any]:
    paths.ensure()
    with RunLock(paths.scheduler / "pipeline.lock"):
        _recover_pending_activation(paths, importer=importer)
        return _publish_reviewed_release_unlocked(
            paths,
            run_id=run_id,
            candidate_parts=candidate_parts,
            review=review,
            policy=policy,
            importer=importer,
        )


def _source_due(source: dict[str, Any], profile: str) -> bool:
    if not source["enabled"]:
        return False
    if profile == "monthly":
        return source["schedule"] in {"daily", "weekly", "monthly"}
    if profile in {"weekly", "retry"}:
        return source["schedule"] in {"daily", "weekly"}
    return False


def locked_source_result(source: dict[str, Any], data_root: Path) -> dict[str, Any]:
    """验证 registry 中的锁定数据集/包确实与 sources.lock.json 一致。"""
    lock_names = {"pc_part_dataset": "pc-part-dataset", "dbgpu": "dbgpu"}
    lock_name = lock_names.get(source["id"])
    if lock_name is None:
        raise PipelineError("lock_mapping_missing", f"来源 {source['id']} 缺少 lock 映射")
    locked = get_source(lock_name, data_root / "sources.lock.json")
    if source["adapter"] == "locked_git_version":
        commit = locked.get("commit")
        if not isinstance(commit, str) or len(commit) != 40 or any(ch not in "0123456789abcdef" for ch in commit):
            raise PipelineError("locked_version_invalid", f"来源 {source['id']} commit 非法")
        return {"source_id": source["id"], "status": "locked_version_verified", "version": commit}
    if source["adapter"] == "locked_package":
        package = locked.get("package")
        expected = locked.get("version")
        try:
            installed = importlib.metadata.version(package)
        except importlib.metadata.PackageNotFoundError as exc:
            raise PipelineError("locked_package_missing", f"锁定包 {package} 未安装") from exc
        if installed != expected:
            raise PipelineError(
                "locked_package_mismatch",
                f"锁定包 {package} 期望 {expected}，实际 {installed}",
            )
        return {"source_id": source["id"], "status": "locked_version_verified", "version": expected}
    raise PipelineError("adapter_mismatch", f"来源 {source['id']} 不是锁定版本适配器")


def _write_run_manifest(run_dir: Path, manifest: dict[str, Any]) -> None:
    _atomic_json(run_dir / "manifest.json", manifest)


def run_scheduled(
    paths: DataPaths,
    *,
    profile: str,
    trigger: str,
    scheduled_for: datetime,
    registry_path: Path = DEFAULT_REGISTRY_PATH,
    collector: HTTPCollector | None = None,
    importer: Importer | None = None,
) -> dict[str, Any]:
    """执行一次定时主链；同 profile/scheduled_for 在同一工作区幂等。"""
    if profile not in {"health", "weekly", "monthly", "retry"}:
        raise PipelineError("invalid_profile", f"非法 profile {profile!r}")
    if trigger not in {"manual", "schedule", "startup_catch_up"}:
        raise PipelineError("invalid_trigger", f"非法 trigger {trigger!r}")
    paths.ensure()
    scheduled = scheduled_for.astimezone(UTC).replace(microsecond=0)
    run_id = str(
        uuid.uuid5(
            uuid.NAMESPACE_URL,
            f"pcdata:{paths.repo_root.resolve()}:{profile}:{scheduled.isoformat()}",
        )
    )
    run_dir = paths.runs / run_id
    final_manifest_path = run_dir / "manifest.json"

    with RunLock(paths.scheduler / "pipeline.lock"):
        recovered = _recover_pending_activation(paths, importer=importer)
        if final_manifest_path.exists():
            existing = json.loads(final_manifest_path.read_text(encoding="utf-8"))
            if existing.get("status") != "running":
                retryable = existing.get("status") == "failed" and (
                    trigger == "startup_catch_up" or profile == "retry"
                )
                if not retryable:
                    return existing
                if recovered is None:
                    # 启动补跑/失败来源重试复用同一幂等 run_id，但重新尝试安全主链。
                    pass
                elif recovered.get("run_id") == run_id:
                    existing.update(
                        {
                            "status": "published",
                            "finished_at": _rfc3339(),
                            "summary": {"decision": "recovered", "release_id": recovered["release_id"]},
                            "error": None,
                        }
                    )
                    _write_run_manifest(run_dir, existing)
                    return existing
        started = _rfc3339()
        manifest: dict[str, Any] = {
            "schema_version": SCHEMA_VERSION,
            "run_id": run_id,
            "profile": profile,
            "trigger": trigger,
            "scheduled_for": _rfc3339(scheduled),
            "started_at": started,
            "finished_at": None,
            "status": "running",
            "model_used": False,
            "sources": [],
            "summary": {},
            "error": None,
        }
        run_dir.mkdir(parents=True, exist_ok=True)
        _write_run_manifest(run_dir, manifest)
        try:
            if profile == "health":
                report = health_report(paths)
                manifest["status"] = "no_change" if report["healthy"] else "blocked"
                manifest["summary"] = report
            else:
                sources = load_registry(registry_path)
                checkpoints = CheckpointStore(paths.checkpoints)
                http = collector or HTTPCollector()
                for source in sources.values():
                    if not _source_due(source, profile):
                        continue
                    if source["adapter"] == "local_parts":
                        manifest["sources"].append(
                            {"source_id": source["id"], "status": "collected", "content_sha256": _parts_digest(paths.seed_parts)}
                        )
                    elif source["adapter"] == "http_snapshot":
                        manifest["sources"].append(
                            http.collect(source, run_dir / "raw", checkpoints)
                        )
                    else:
                        # 锁定数据集/包只记录当前锁，升级必须显式改 sources.lock.json。
                        manifest["sources"].append(locked_source_result(source, paths.data_root))

                base_release_id, base_parts = _current_parts(paths)
                candidate = run_dir / "normalized" / "parts"
                _copy_parts(base_parts or paths.seed_parts, candidate)
                review = create_review(
                    run_id=run_id,
                    candidate_parts=candidate,
                    base_parts=base_parts,
                    base_release_id=base_release_id,
                    model_used=False,
                )
                _write_review(run_dir, review)
                if review["decision"] == "no_change":
                    manifest["status"] = "no_change"
                    manifest["summary"] = {"decision": "no_change", "changes": 0}
                elif review["decision"] == "auto_publish":
                    release = _publish_reviewed_release_unlocked(
                        paths,
                        run_id=run_id,
                        candidate_parts=candidate,
                        review=review,
                        policy="auto",
                        importer=importer,
                    )
                    manifest["status"] = "published"
                    manifest["summary"] = {"decision": "auto_publish", "release_id": release["release_id"]}
                else:
                    quarantine_dir = paths.quarantine / run_id
                    quarantine_dir.mkdir(parents=True, exist_ok=True)
                    shutil.copy2(run_dir / "review.json", quarantine_dir / "review.json")
                    shutil.copy2(run_dir / "review.md", quarantine_dir / "review.md")
                    manifest["status"] = "blocked" if review["decision"] == "manual_required" else "quarantined"
                    manifest["summary"] = {
                        "decision": review["decision"],
                        "changes": len(review["changes"]),
                        "risks": len(review["risks"]),
                    }
        except (PipelineError, SpecError, OSError, json.JSONDecodeError) as exc:
            code = exc.code if isinstance(exc, PipelineError) else "pipeline_failed"
            manifest["status"] = "failed"
            manifest["error"] = {"code": code, "message": str(exc)}
        manifest["finished_at"] = _rfc3339()
        _write_run_manifest(run_dir, manifest)
        return manifest


def health_report(paths: DataPaths) -> dict[str, Any]:
    paths.ensure()
    problems: list[dict[str, str]] = []
    current = None
    try:
        current = _read_current(paths)
    except PipelineError as exc:
        problems.append({"code": exc.code, "message": str(exc)})
    if current is None:
        problems.append({"code": "baseline_missing", "message": "尚未建立初始 last-known-good"})
    if paths.pending_activation.exists():
        problems.append({"code": "pending_activation", "message": "存在尚未完成的数据库/文件发布恢复记录"})
    pending = sum(1 for path in paths.quarantine.iterdir() if path.is_dir())
    return {
        "schema_version": SCHEMA_VERSION,
        "checked_at": _rfc3339(),
        "healthy": not problems,
        "current_release_id": current["release_id"] if current else None,
        "pending_quarantine": pending,
        "problems": problems,
    }
