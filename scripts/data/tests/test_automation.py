"""P11 自动运行、HTTP 安全、风险分类和 last-known-good。"""

import hashlib
import json
import shutil
import urllib.error
from email.message import Message
from pathlib import Path

import pytest

from pcdata.automation import (
    CheckpointStore,
    DataPaths,
    HTTPCollector,
    PipelineError,
    RunLock,
    _copy_parts,
    _current_evidence,
    _load_parts,
    _write_evidence,
    bootstrap_release,
    create_review,
    health_report,
    locked_source_result,
    publish_reviewed_release,
    run_scheduled,
    stable_json_bytes,
)

REAL_DATA = Path(__file__).resolve().parents[1]


@pytest.fixture
def paths(tmp_path):
    repo = tmp_path / "repo"
    data = repo / "scripts" / "data"
    data.mkdir(parents=True)
    shutil.copytree(REAL_DATA / "parts", data / "parts")
    shutil.copytree(REAL_DATA / "prices", data / "prices")
    return DataPaths(repo_root=repo, data_root=data, runtime_root=repo / "var" / "data")


class FakeResponse:
    def __init__(self, body: bytes, *, status=200, content_type="text/html", extra_headers=None):
        self.body = body
        self.status = status
        self.headers = Message()
        self.headers["Content-Type"] = content_type
        for key, value in (extra_headers or {}).items():
            self.headers[key] = value

    def getcode(self):
        return self.status

    def read(self, amount):
        return self.body[:amount]

    def __enter__(self):
        return self

    def __exit__(self, *_args):
        return None


def http_source(**overrides):
    source = {
        "id": "vendor_specs",
        "adapter": "http_snapshot",
        "base_url": "http://127.0.0.1/specs",
        "enabled": True,
        "automated_access": "allowed",
    }
    source.update(overrides)
    return source


def test_stable_json拒绝敏感字段并保持lf():
    data = stable_json_bytes({"schema_version": 1, "中文": "正常"})
    assert data.endswith(b"\n") and b"\r" not in data and not data.startswith(b"\xef\xbb\xbf")
    with pytest.raises(PipelineError, match="禁止字段"):
        stable_json_bytes({"api_key": "not-even-a-real-key"})
    with pytest.raises(PipelineError, match="疑似包含凭据"):
        stable_json_bytes({"message": "sk-example"})


def test_http采集保存哈希和条件请求(tmp_path):
    requests = []

    def opener(request, timeout):
        requests.append((request, timeout))
        if request.full_url.endswith("/robots.txt"):
            return FakeResponse(b"", content_type="text/plain")
        return FakeResponse(
            b"<html><title>Specs</title></html>",
            extra_headers={"ETag": '"v1"', "Last-Modified": "Sun, 24 Aug 2026 00:00:00 GMT"},
        )

    store = CheckpointStore(tmp_path / "checkpoints")
    collector = HTTPCollector(opener=opener, allow_http_for_test=True)
    first = collector.collect(http_source(), tmp_path / "raw", store)
    second = collector.collect(http_source(), tmp_path / "raw", store)
    assert first["status"] == "collected"
    assert second["status"] == "not_modified"
    assert first["source_url"] == "http://127.0.0.1/specs"
    assert len(list((tmp_path / "raw").iterdir())) == 1
    assert requests[-1][0].get_header("If-none-match") == '"v1"'


def test_http_304与访问限制(tmp_path):
    store = CheckpointStore(tmp_path / "checkpoints")
    store.save("vendor_specs", {"etag": '"v1"'})

    def not_modified(request, timeout):
        if request.full_url.endswith("/robots.txt"):
            return FakeResponse(b"User-agent: *\nAllow: /\n", content_type="text/plain")
        raise urllib.error.HTTPError(request.full_url, 304, "Not Modified", Message(), None)

    result = HTTPCollector(opener=not_modified, allow_http_for_test=True).collect(
        http_source(), tmp_path / "raw", store
    )
    assert result["status"] == "not_modified"

    def rate_limited(request, timeout):
        raise urllib.error.HTTPError(request.full_url, 429, "Too Many", Message(), None)

    with pytest.raises(PipelineError, match="HTTP 429") as error:
        HTTPCollector(opener=rate_limited, allow_http_for_test=True).collect(
            http_source(), tmp_path / "raw", store
        )
    assert error.value.code == "rate_limited"


def test_http_redirect_ssrf与失败检查点(tmp_path):
    def redirect(request, timeout):
        if request.full_url.endswith("/robots.txt"):
            return FakeResponse(b"User-agent: *\nAllow: /\n", content_type="text/plain")
        raise urllib.error.HTTPError(request.full_url, 302, "Found", Message({"Location": "https://127.0.0.1/"}), None)

    store = CheckpointStore(tmp_path / "checkpoints")
    with pytest.raises(PipelineError) as error:
        HTTPCollector(opener=redirect, allow_http_for_test=True).collect(
            http_source(), tmp_path / "raw", store
        )
    assert error.value.code == "redirect_blocked"
    checkpoint = store.load("vendor_specs")
    assert checkpoint["last_attempt_at"] and checkpoint["status"] == "redirect_blocked"

    with pytest.raises(PipelineError) as error:
        HTTPCollector(opener=lambda *_args, **_kwargs: pytest.fail("SSRF 目标不得请求")).collect(
            http_source(base_url="https://127.0.0.1/specs"), tmp_path / "raw", store
        )
    assert error.value.code == "ssrf_blocked"


def test_http只对幂等临时错误有限重试(tmp_path):
    attempts = 0

    def opener(request, timeout):
        nonlocal attempts
        if request.full_url.endswith("/robots.txt"):
            return FakeResponse(b"User-agent: *\nAllow: /\n", content_type="text/plain")
        attempts += 1
        if attempts == 1:
            raise urllib.error.HTTPError(request.full_url, 503, "Unavailable", Message(), None)
        return FakeResponse(b"ok", content_type="text/plain")

    result = HTTPCollector(opener=opener, allow_http_for_test=True).collect(
        http_source(), tmp_path / "raw", CheckpointStore(tmp_path / "checkpoints")
    )
    assert result["status"] == "collected" and attempts == 2


def test_http遵守robots禁止(tmp_path):
    def opener(request, timeout):
        if request.full_url.endswith("/robots.txt"):
            return FakeResponse(b"User-agent: *\nDisallow: /\n", content_type="text/plain")
        raise AssertionError("robots 禁止后不得请求业务页面")

    with pytest.raises(PipelineError) as error:
        HTTPCollector(opener=opener, allow_http_for_test=True).collect(
            http_source(), tmp_path / "raw", CheckpointStore(tmp_path / "checkpoints")
        )
    assert error.value.code == "robots_disallowed"


def test_http登录页_超大响应和非https失败(tmp_path):
    store = CheckpointStore(tmp_path / "checkpoints")
    login = HTTPCollector(
        opener=lambda *_args, **_kwargs: FakeResponse("请输入验证码".encode()),
        allow_http_for_test=True,
    )
    with pytest.raises(PipelineError) as error:
        login.collect(http_source(), tmp_path / "raw", store)
    assert error.value.code == "access_challenge"

    def huge_opener(request, timeout):
        if request.full_url.endswith("/robots.txt"):
            return FakeResponse(b"", content_type="text/plain")
        return FakeResponse(b"12345")

    huge = HTTPCollector(max_bytes=4, opener=huge_opener, allow_http_for_test=True)
    with pytest.raises(PipelineError) as error:
        huge.collect(http_source(), tmp_path / "raw", store)
    assert error.value.code == "response_too_large"

    secure = HTTPCollector(opener=lambda *_args, **_kwargs: FakeResponse(b"ok"))
    with pytest.raises(PipelineError) as error:
        secure.collect(http_source(), tmp_path / "raw", store)
    assert error.value.code == "insecure_source"


def _candidate(paths: DataPaths, run_id: str) -> Path:
    target = paths.runs / run_id / "normalized" / "parts"
    _copy_parts(paths.seed_parts, target)
    return target


def _rewrite_record(parts_dir: Path, sku: str, mutate):
    for path in parts_dir.glob("*.jsonl"):
        rows = [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines()]
        found = False
        for row in rows:
            if row["sku"] == sku:
                mutate(row)
                found = True
        if found:
            path.write_text(
                "".join(json.dumps(row, ensure_ascii=False, separators=(", ", ": ")) + "\n" for row in rows),
                encoding="utf-8",
                newline="\n",
            )
            return
    raise AssertionError(f"找不到 {sku}")


def test_bootstrap幂等并建立last_known_good(paths):
    imported = []
    importer = lambda repo, release: imported.append((repo, release))
    first = bootstrap_release(paths, importer=importer)
    second = bootstrap_release(paths, importer=importer)
    assert first["release_id"] == second["release_id"]
    assert len(imported) == 1
    release = paths.releases / first["release_id"]
    assert len(_load_parts(release / "parts")) == 160
    assert health_report(paths)["healthy"]


def test_bootstrap导入失败不切换current(paths):
    def fail(_repo, _release):
        raise PipelineError("database_import_failed", "boom")

    with pytest.raises(PipelineError):
        bootstrap_release(paths, importer=fail)
    assert not paths.current.exists()
    assert health_report(paths)["problems"][0]["code"] == "baseline_missing"


def test_release文件损坏后health失败(paths):
    current = bootstrap_release(paths, importer=lambda *_args: None)
    target = paths.releases / current["release_id"] / "parts" / "cpu.jsonl"
    target.write_bytes(target.read_bytes() + b"\n")
    report = health_report(paths)
    assert not report["healthy"]
    assert report["problems"][0]["code"] == "release_invalid"


def test_release_v2_evidence_only发布且篡改可检测(paths):
    current = bootstrap_release(paths, importer=lambda *_args: None)
    run_id = "12121212-1212-4212-8212-121212121212"
    candidate = _candidate(paths, run_id)
    evidence_path = paths.runs / run_id / "normalized" / "evidence.jsonl"
    identity = {
        "source_id": "amd_products",
        "sku": "cpu-r5-7600",
        "field": "specs.socket",
        "value": "AM5",
        "raw_sha256": "b" * 64,
    }
    evidence_id = hashlib.sha256(
        json.dumps(identity, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
    ).hexdigest()
    evidence = {
        "schema_version": 1,
        "id": evidence_id,
        "sku": "cpu-r5-7600",
        "field": "specs.socket",
        "value": "AM5",
        "source_id": "amd_products",
        "source_url": "https://www.amd.com/en/products/example.html",
        "captured_at": "2026-08-24T00:00:00Z",
        "raw_sha256": "b" * 64,
        "method": "deterministic",
        "evidence_excerpt": "CPU Socket: AM5",
        "evidence_status": "verified",
    }
    _write_evidence(evidence_path, [evidence])
    base = paths.releases / current["release_id"] / "parts"
    review = create_review(
        run_id=run_id,
        candidate_parts=candidate,
        base_parts=base,
        base_release_id=current["release_id"],
        candidate_evidence=evidence_path,
        base_evidence=_current_evidence(paths, current["release_id"]),
    )
    assert review["decision"] == "auto_publish"
    release = publish_reviewed_release(
        paths,
        run_id=run_id,
        candidate_parts=candidate,
        candidate_evidence=evidence_path,
        review=review,
        policy="auto",
        importer=lambda *_args: None,
    )
    assert release["schema_version"] == 2
    stored = paths.releases / release["release_id"] / "evidence" / "fields.jsonl"
    stored.write_bytes(stored.read_bytes() + b"\n")
    report = health_report(paths)
    assert not report["healthy"]
    assert report["problems"][0]["code"] == "release_invalid"


def test_current路径穿越被拒绝(paths):
    paths.ensure()
    paths.current.write_text(
        json.dumps(
            {
                "schema_version": 1,
                "release_id": "../outside",
                "manifest_sha256": "a" * 64,
                "updated_at": "2026-08-24T00:00:00Z",
            }
        ),
        encoding="utf-8",
    )
    report = health_report(paths)
    assert not report["healthy"]
    assert report["problems"][0]["code"] == "current_invalid"


def test_review_no_change_低风险和关键字段(paths):
    current = bootstrap_release(paths, importer=lambda *_args: None)
    base = paths.releases / current["release_id"] / "parts"

    same = _candidate(paths, "11111111-1111-4111-8111-111111111111")
    review = create_review(
        run_id="11111111-1111-4111-8111-111111111111",
        candidate_parts=same,
        base_parts=base,
        base_release_id=current["release_id"],
    )
    assert review["decision"] == "no_change"

    low = _candidate(paths, "22222222-2222-4222-8222-222222222222")
    _rewrite_record(low, "cpu-r5-5500", lambda row: row["source_meta"].update({"reviewed": "2026-08-24"}))
    review = create_review(
        run_id="22222222-2222-4222-8222-222222222222",
        candidate_parts=low,
        base_parts=base,
        base_release_id=current["release_id"],
    )
    assert review["decision"] == "auto_publish" and not review["risks"]

    critical = _candidate(paths, "33333333-3333-4333-8333-333333333333")
    _rewrite_record(critical, "cpu-r5-5500", lambda row: row["specs"].update({"tdp_w": 66}))
    review = create_review(
        run_id="33333333-3333-4333-8333-333333333333",
        candidate_parts=critical,
        base_parts=base,
        base_release_id=current["release_id"],
    )
    assert review["decision"] == "quarantine"
    assert review["risks"][0]["code"] == "critical_identity_or_spec_changed"


def test_model参与和隐式删除必须隔离(paths):
    current = bootstrap_release(paths, importer=lambda *_args: None)
    base = paths.releases / current["release_id"] / "parts"
    candidate = _candidate(paths, "44444444-4444-4444-8444-444444444444")
    cpu = candidate / "cpu.jsonl"
    cpu.write_text("\n".join(cpu.read_text(encoding="utf-8").splitlines()[1:]) + "\n", encoding="utf-8", newline="\n")
    review = create_review(
        run_id="44444444-4444-4444-8444-444444444444",
        candidate_parts=candidate,
        base_parts=base,
        base_release_id=current["release_id"],
        model_used=True,
    )
    codes = {risk["code"] for risk in review["risks"]}
    assert {"implicit_removal", "model_output_requires_manual_review"} <= codes
    assert review["decision"] == "quarantine"

    explicit = _candidate(paths, "45454545-4545-4454-8454-454545454545")
    _rewrite_record(explicit, "cpu-r5-5500", lambda row: row.update({"catalog_state": "retired"}))
    review = create_review(
        run_id="45454545-4545-4454-8454-454545454545",
        candidate_parts=explicit,
        base_parts=base,
        base_release_id=current["release_id"],
    )
    assert review["decision"] == "quarantine"
    assert any(risk["code"] == "catalog_state_changed" for risk in review["risks"])


def test_publish检查漂移且失败不切换指针(paths):
    current = bootstrap_release(paths, importer=lambda *_args: None)
    candidate = _candidate(paths, "55555555-5555-4555-8555-555555555555")
    _rewrite_record(candidate, "cpu-r5-5500", lambda row: row["source_meta"].update({"reviewed": "new"}))
    base = paths.releases / current["release_id"] / "parts"
    review = create_review(
        run_id="55555555-5555-4555-8555-555555555555",
        candidate_parts=candidate,
        base_parts=base,
        base_release_id=current["release_id"],
    )
    _rewrite_record(candidate, "cpu-r5-5500", lambda row: row["source_meta"].update({"drift": True}))
    with pytest.raises(PipelineError) as error:
        publish_reviewed_release(
            paths,
            run_id=review["run_id"],
            candidate_parts=candidate,
            review=review,
            policy="auto",
            importer=lambda *_args: None,
        )
    assert error.value.code == "review_drift"
    assert json.loads(paths.current.read_text(encoding="utf-8"))["release_id"] == current["release_id"]


def test_publish拒绝过期review(paths):
    current = bootstrap_release(paths, importer=lambda *_args: None)
    candidate = _candidate(paths, "66666666-6666-4666-8666-666666666666")
    _rewrite_record(candidate, "cpu-r5-5500", lambda row: row["source_meta"].update({"reviewed": "new"}))
    review = create_review(
        run_id="66666666-6666-4666-8666-666666666666",
        candidate_parts=candidate,
        base_parts=paths.releases / current["release_id"] / "parts",
        base_release_id=current["release_id"],
    )
    publish_reviewed_release(
        paths,
        run_id=review["run_id"],
        candidate_parts=candidate,
        review=review,
        policy="auto",
        importer=lambda *_args: None,
    )
    with pytest.raises(PipelineError, match="基线已过期"):
        publish_reviewed_release(
            paths,
            run_id=review["run_id"],
            candidate_parts=candidate,
            review=review,
            policy="auto",
            importer=lambda *_args: None,
        )


def test_current切换失败补偿回滚数据库投影(paths, monkeypatch):
    current = bootstrap_release(paths, importer=lambda *_args: None)
    candidate = _candidate(paths, "77777777-7777-4777-8777-777777777777")
    _rewrite_record(candidate, "cpu-r5-5500", lambda row: row["source_meta"].update({"reviewed": "new"}))
    review = create_review(
        run_id="77777777-7777-4777-8777-777777777777",
        candidate_parts=candidate,
        base_parts=paths.releases / current["release_id"] / "parts",
        base_release_id=current["release_id"],
    )
    imported: list[str] = []

    def importer(_repo, release):
        imported.append(release.name)

    def fail_activation(_paths, _manifest):
        raise OSError("simulated current lock")

    monkeypatch.setattr("pcdata.automation._activate_release", fail_activation)
    with pytest.raises(PipelineError, match="current.json"):
        publish_reviewed_release(
            paths,
            run_id=review["run_id"],
            candidate_parts=candidate,
            review=review,
            policy="auto",
            importer=importer,
        )
    assert imported[-1] == current["release_id"]
    assert json.loads(paths.current.read_text(encoding="utf-8"))["release_id"] == current["release_id"]
    assert not paths.pending_activation.exists()


def test_scheduled_run无变化_幂等且不调用导入(paths):
    bootstrap_release(paths, importer=lambda *_args: None)
    imports = []
    when = __import__("datetime").datetime(2026, 8, 24, 4, 0, tzinfo=__import__("datetime").UTC)
    first = run_scheduled(
        paths,
        profile="weekly",
        trigger="schedule",
        scheduled_for=when,
        registry_path=REAL_DATA / "sources.registry.json",
        importer=lambda *args: imports.append(args),
    )
    second = run_scheduled(
        paths,
        profile="weekly",
        trigger="startup_catch_up",
        scheduled_for=when,
        registry_path=REAL_DATA / "sources.registry.json",
        importer=lambda *args: imports.append(args),
    )
    assert first["run_id"] == second["run_id"]
    assert first["status"] == second["status"] == "partial"
    assert first["summary"] == second["summary"]
    assert first["trigger"] == "schedule"
    assert second["trigger"] == "startup_catch_up"
    assert first["model_used"] is False
    assert imports == []


def test_run_lock阻止并发(tmp_path):
    path = tmp_path / "pipeline.lock"
    with RunLock(path):
        with pytest.raises(PipelineError) as error:
            with RunLock(path):
                pass
        assert error.value.code == "already_running"
    assert path.exists()  # 锁文件可残留，但 OS 锁已释放，下次任务能重新获取。
    with RunLock(path):
        pass


def test_锁定数据集和包版本被真实校验():
    registry = json.loads((REAL_DATA / "sources.registry.json").read_text(encoding="utf-8"))
    sources = {source["id"]: source for source in registry["sources"]}
    dataset = locked_source_result(sources["pc_part_dataset"], REAL_DATA)
    package = locked_source_result(sources["dbgpu"], REAL_DATA)
    assert dataset["status"] == package["status"] == "locked_version_verified"
    assert len(dataset["version"]) == 40
    assert package["version"] == "2025.12"
