"""P12B SerpApi Free / Baidu Shopping 低频价格观察适配器。

只读取 SerpApi JSON，不访问搜索结果中的商城链接，也不调用模型。
"""

from __future__ import annotations

import hashlib
import json
import os
import re
import subprocess
import unicodedata
import uuid
from dataclasses import dataclass
from datetime import UTC, datetime
from decimal import Decimal, InvalidOperation
from pathlib import Path
from time import time
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.parse import parse_qs, urlencode, urlsplit, urlunsplit
from urllib.request import Request, urlopen

from .automation import DataPaths, PipelineError, stable_json_bytes
from .coverage import load_parts_jsonl
from .prices import (
    _atomic_json,
    _observation_identity,
    _rfc3339,
    _write_jsonl,
    create_price_review,
    import_price_observations,
    publish_price_review,
)

COLLECTOR_ID = "serpapi_baidu"
DEFAULT_BASE_URL = "https://serpapi.com"
PRICE_PATTERN = re.compile(r"^[￥¥]?\s*([0-9]{1,8}(?:,[0-9]{3})*(?:\.[0-9]{1,2})?)\s*(?:元|CNY)?$", re.IGNORECASE)
CONDITIONAL_PRICE_WORDS = ("起", "至", "-", "~", "月供", "每月", "定金", "订金", "券后", "会员", "满减")
TRACKING_KEYS = {"spm", "utm_source", "utm_medium", "utm_campaign", "utm_term", "utm_content", "from", "source"}
TOKEN_STOP = {"oc", "edition", "gaming", "desktop", "processor", "cpu", "gpu", "graphics", "card", "series"}
BRAND_ALIASES = {
    "Asus": ["asus", "华硕"], "MSI": ["msi", "微星"], "Gigabyte": ["gigabyte", "技嘉"],
    "Sapphire": ["sapphire", "蓝宝石"], "Intel": ["intel", "英特尔"], "AMD": ["amd", "锐龙"],
    "Corsair": ["corsair", "海盗船", "美商海盗船"], "G.Skill": ["gskill", "芝奇"],
    "Kingston": ["kingston", "金士顿"], "TeamGroup": ["teamgroup", "十铨"], "Western Digital": ["wd", "西部数据"],
    "Samsung": ["samsung", "三星"], "Crucial": ["crucial", "英睿达"], "ADATA": ["adata", "威刚"],
    "Seasonic": ["seasonic", "海韵"], "be quiet!": ["bequiet", "德商德静界"], "Thermaltake": ["thermaltake", "曜越"],
    "Lian Li": ["lianli", "联力"], "Cooler Master": ["coolermaster", "酷冷至尊"], "Fractal Design": ["fractal", "分形工艺"],
    "NZXT": ["nzxt", "恩杰"], "Montech": ["montech", "君主"], "Arctic": ["arctic"],
    "DeepCool": ["deepcool", "九州风神"], "Noctua": ["noctua", "猫头鹰"], "Thermalright": ["thermalright", "利民"],
    "ASRock": ["asrock", "华擎"],
}


def _normalize(value: str) -> str:
    value = unicodedata.normalize("NFKC", value).lower().replace("®", "").replace("™", "")
    return "".join(character for character in value if character.isalnum())


def _tokens(value: str) -> list[str]:
    return [token for token in re.findall(r"[a-z0-9]+", value.lower()) if len(token) >= 2 and token not in TOKEN_STOP]


def _safe_base_url(value: str) -> str:
    parsed = urlsplit(value.rstrip("/"))
    loopback = parsed.hostname in {"127.0.0.1", "localhost", "::1"}
    if parsed.scheme != "https" and not (parsed.scheme == "http" and loopback):
        raise PipelineError("serpapi_invalid_base_url", "SERPAPI_BASE_URL 必须为 HTTPS；测试仅允许 loopback HTTP")
    if not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment:
        raise PipelineError("serpapi_invalid_base_url", "SERPAPI_BASE_URL 非法")
    return value.rstrip("/")


@dataclass(frozen=True)
class SerpApiSettings:
    api_key: str
    base_url: str
    monthly_budget: int
    daily_skus: int
    require_free_plan: bool

    @classmethod
    def from_env(cls) -> "SerpApiSettings":
        key = os.getenv("SERPAPI_API_KEY", "").strip()
        if not key:
            raise PipelineError("serpapi_key_missing", "SERPAPI_API_KEY 未设置")
        try:
            budget = int(os.getenv("SERPAPI_MONTHLY_REQUEST_BUDGET", "240"))
            daily = int(os.getenv("SERPAPI_DAILY_SKUS", "7"))
        except ValueError as exc:
            raise PipelineError("serpapi_invalid_config", "SerpApi 预算和每日 SKU 必须为整数") from exc
        if not 1 <= budget <= 240 or not 1 <= daily <= 12:
            raise PipelineError("serpapi_invalid_config", "月预算必须为 1..240，每日 SKU 必须为 1..12")
        require_free = os.getenv("SERPAPI_REQUIRE_FREE_PLAN", "true").strip().lower() not in {"0", "false", "no"}
        return cls(key, _safe_base_url(os.getenv("SERPAPI_BASE_URL", DEFAULT_BASE_URL)), budget, daily, require_free)


def _load_local_env(paths: DataPaths) -> None:
    """只补充 P12B 所需键；不覆盖调用进程显式环境变量。"""
    allowed = {
        "SERPAPI_API_KEY", "SERPAPI_BASE_URL", "SERPAPI_MONTHLY_REQUEST_BUDGET",
        "SERPAPI_DAILY_SKUS", "SERPAPI_REQUIRE_FREE_PLAN",
    }
    try:
        lines = (paths.repo_root / ".env").read_text(encoding="utf-8-sig").splitlines()
    except OSError:
        return
    for line in lines:
        stripped = line.strip()
        if not stripped or stripped.startswith("#") or "=" not in stripped:
            continue
        key, value = stripped.split("=", 1)
        key = key.strip()
        if key in allowed and key not in os.environ:
            os.environ[key] = value.strip().strip('"').strip("'")


class SerpApiClient:
    def __init__(self, settings: SerpApiSettings, *, timeout_seconds: float = 20) -> None:
        self.settings = settings
        self.timeout_seconds = timeout_seconds

    def _get(self, path: str, params: dict[str, str]) -> tuple[dict[str, Any], bytes]:
        query = urlencode({**params, "api_key": self.settings.api_key})
        request = Request(f"{self.settings.base_url}{path}?{query}", headers={"Accept": "application/json", "User-Agent": "pc-builder-agent-price/1"})
        try:
            with urlopen(request, timeout=self.timeout_seconds) as response:  # noqa: S310 - URL 已受配置门禁约束
                body = response.read(2 * 1024 * 1024 + 1)
        except HTTPError as exc:
            code = {401: "authentication", 403: "quota_or_forbidden", 429: "rate_limit"}.get(exc.code, "upstream_http")
            raise PipelineError(f"serpapi_{code}", f"SerpApi 请求失败（HTTP {exc.code}）") from None
        except (TimeoutError, URLError) as exc:
            raise PipelineError("serpapi_unavailable", f"SerpApi 请求不可用（{type(exc).__name__}）") from None
        if len(body) > 2 * 1024 * 1024:
            raise PipelineError("serpapi_response_too_large", "SerpApi 响应超过 2 MiB")
        try:
            value = json.loads(body)
        except json.JSONDecodeError as exc:
            raise PipelineError("serpapi_protocol", "SerpApi 返回的不是 JSON") from exc
        if not isinstance(value, dict):
            raise PipelineError("serpapi_protocol", "SerpApi JSON 顶层必须为对象")
        return value, body

    def account(self, requested: int) -> dict[str, int | str]:
        payload, _ = self._get("/account.json", {})
        plan = str(payload.get("plan_name") or payload.get("plan") or "")
        usage = _first_int(payload, "this_month_usage", "searches_this_month", "total_searches")
        remaining = _first_int(payload, "plan_searches_left", "searches_left", "total_searches_left")
        if not plan or usage is None or remaining is None:
            raise PipelineError("serpapi_account_unreadable", "无法读取 SerpApi 套餐或额度")
        if self.settings.require_free_plan and "free" not in plan.lower():
            raise PipelineError("serpapi_not_free_plan", "SerpApi 当前不是 Free 套餐，已停止自动采集")
        if usage + requested > self.settings.monthly_budget or remaining < requested:
            raise PipelineError("serpapi_budget_exhausted", "SerpApi 免费额度或项目月度硬预算不足")
        return {"plan": plan, "usage": usage, "remaining": remaining}

    def search(self, query: str) -> tuple[dict[str, Any], bytes]:
        return self._get("/search.json", {"engine": "baidu", "q": query, "device": "mobile"})


def _first_int(payload: dict[str, Any], *keys: str) -> int | None:
    for key in keys:
        value = payload.get(key)
        if isinstance(value, int) and value >= 0:
            return value
        if isinstance(value, str) and value.isdigit():
            return int(value)
    return None


def _load_config(paths: DataPaths) -> tuple[dict[str, Any], dict[str, dict[str, Any]]]:
    config = json.loads((paths.data_root / "serpapi-baidu-products.json").read_text(encoding="utf-8"))
    if config.get("schema_version") != 1 or config.get("activation_status") not in {"pending_canary", "active"}:
        raise PipelineError("serpapi_mapping_invalid", "SerpApi 商品映射配置非法")
    records: dict[str, dict[str, Any]] = {}
    for file in sorted(paths.seed_parts.glob("*.jsonl")):
        for record in load_parts_jsonl(file):
            records[str(record["sku"])] = record
    flattened = [sku for values in config["core_skus"].values() for sku in values]
    if len(flattened) != 96 or len(set(flattened)) != 96 or any(sku not in records for sku in flattened):
        raise PipelineError("serpapi_mapping_invalid", "核心池必须是存在于目录中的 96 个唯一 SKU")
    if any(len(values) != 12 for values in config["core_skus"].values()) or len(config["canary_skus"]) != 12:
        raise PipelineError("serpapi_mapping_invalid", "核心池每类和 canary 均必须为 12 个 SKU")
    if set(config["canary_skus"]) - set(flattened):
        raise PipelineError("serpapi_mapping_invalid", "canary SKU 必须属于核心池")
    for category, skus in config["core_skus"].items():
        if any(records[sku].get("category") != category for sku in skus):
            raise PipelineError("serpapi_mapping_invalid", f"{category} 核心池包含错误品类")
    if set(config.get("overrides", {})) - set(flattened):
        raise PipelineError("serpapi_mapping_invalid", "override 只能引用核心池 SKU")
    products = config.get("products")
    if not isinstance(products, dict) or set(products) != set(flattened):
        raise PipelineError("serpapi_mapping_invalid", "必须固化全部 96 个 SKU 的查询与身份映射")
    for sku, identity in products.items():
        if not isinstance(identity, dict) or set(identity) != {"query", "brand_aliases", "required_tokens", "forbidden_tokens"}:
            raise PipelineError("serpapi_mapping_invalid", f"{sku} 身份映射字段非法")
        if not identity["query"] or not identity["brand_aliases"] or not identity["required_tokens"]:
            raise PipelineError("serpapi_mapping_invalid", f"{sku} 身份映射不得为空")
    return config, records


def _derived_identity(config: dict[str, Any], part: dict[str, Any]) -> dict[str, Any]:
    override = config.get("overrides", {}).get(part["sku"], {})
    required = override.get("required_tokens") or _tokens(str(part["model"]))
    if not required:
        raise PipelineError("serpapi_mapping_invalid", f"{part['sku']} 没有可用型号 token")
    aliases = override.get("brand_aliases") or BRAND_ALIASES.get(str(part["brand"]), [_normalize(str(part["brand"]))])
    forbidden = list(override.get("forbidden_tokens", []))
    model = _normalize(str(part["model"]))
    if part["category"] == "gpu" and "geforce" in model:
        if "ti" not in model:
            forbidden.append("ti")
        if "super" not in model:
            forbidden.append("super")
    if part["category"] == "gpu" and "radeon" in model and "xt" not in model:
        forbidden.extend(["xt", "xtx"])
    if part["category"] == "cpu" and "ryzen" in model:
        if not re.search(r"\d+x(?:3d)?", model):
            forbidden.append("x")
        if not re.search(r"\d+g", model):
            forbidden.append("g")
    if part["category"] == "ssd":
        if "1tb" in model:
            forbidden.extend(["2tb", "4tb"])
        elif "2tb" in model:
            forbidden.extend(["1tb", "4tb"])
    forbidden = list(dict.fromkeys(forbidden))
    return {
        "query": config["query_template"].format(brand=part["brand"], model=part["model"]),
        "brand_aliases": aliases,
        "required_tokens": required,
        "forbidden_tokens": forbidden,
    }


def _identity(config: dict[str, Any], part: dict[str, Any]) -> dict[str, Any]:
    identity = config["products"].get(part["sku"])
    if not isinstance(identity, dict):
        raise PipelineError("serpapi_mapping_invalid", f"{part['sku']} 缺少固化映射")
    return identity


def _parse_price(value: Any) -> Decimal | None:
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        text = str(value)
    elif isinstance(value, str):
        text = value.strip()
    else:
        return None
    if any(word in text for word in CONDITIONAL_PRICE_WORDS):
        return None
    match = PRICE_PATTERN.fullmatch(text)
    if not match:
        return None
    try:
        amount = Decimal(match.group(1).replace(",", ""))
    except InvalidOperation:
        return None
    if amount <= 0 or amount >= Decimal("100000000") or amount.as_tuple().exponent < -2:
        return None
    return amount.quantize(Decimal("0.01"))


def _normalize_link(value: Any) -> str | None:
    if not isinstance(value, str):
        return None
    parsed = urlsplit(value.strip())
    if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password:
        return None
    query = parse_qs(parsed.query)
    for wrapper in ("o", "url", "target", "redirect", "redirect_url"):
        candidate = query.get(wrapper, [None])[0]
        if candidate:
            nested = urlsplit(candidate)
            if nested.scheme == "https" and nested.hostname and not nested.username and not nested.password:
                parsed = nested
                break
    clean_query = [(key, item) for key, values in parse_qs(parsed.query, keep_blank_values=True).items() if key.lower() not in TRACKING_KEYS for item in values]
    return urlunsplit(("https", parsed.netloc.lower(), parsed.path or "/", urlencode(clean_query), ""))


def _matches(title: str, identity: dict[str, Any]) -> bool:
    normalized = _normalize(title)
    title_tokens = set(re.findall(r"[a-z0-9]+", unicodedata.normalize("NFKC", title).lower()))
    aliases = [_normalize(value) for value in identity["brand_aliases"]]
    return (
        any(alias and alias in normalized for alias in aliases)
        and all(_normalize(token) in normalized for token in identity["required_tokens"])
        and not any(_forbidden_present(_normalize(token), title_tokens) for token in identity["forbidden_tokens"])
    )


def _forbidden_present(forbidden: str, title_tokens: set[str]) -> bool:
    if forbidden in {"ti", "super", "xt", "xtx", "x", "x3d", "g"}:
        return any(token == forbidden or re.search(rf"\d+{re.escape(forbidden)}$", token) for token in title_tokens)
    return forbidden in title_tokens


def _listing_rows(sku: str, payload: dict[str, Any], raw_sha256: str, identity: dict[str, Any], observed_at: str) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    for item in payload.get("shopping_results", []):
        if not isinstance(item, dict):
            continue
        title = str(item.get("title") or "").strip()
        seller = str(item.get("source") or item.get("merchant") or item.get("seller") or "").strip()
        link = _normalize_link(item.get("link") or item.get("product_link"))
        price = _parse_price(item.get("price") or item.get("extracted_price"))
        conditions = " ".join(str(item.get(key) or "") for key in ("tag", "installment", "alternative_price"))
        if any(word in conditions for word in ("月供", "分期", "券后", "会员", "定金", "订金")):
            continue
        if not title or not seller or link is None or price is None or not _matches(title, identity):
            continue
        host = urlsplit(link).hostname or "unknown"
        source_id = f"serpapi_baidu:{_normalize(seller) or host}:{host}"
        product_id = hashlib.sha256(f"{source_id}\0{link}\0{_normalize(title)}".encode()).hexdigest()
        row: dict[str, Any] = {
            "schema_version": 1, "sku": sku, "price_cny": f"{price:.2f}", "currency": "CNY",
            "source_id": source_id, "collector_id": COLLECTOR_ID, "product_id": product_id,
            "source_url": link, "seller": seller, "price_type": "listing",
            "availability_basis": "search_listing", "stock_status": "unknown", "variant_match": "exact",
            "observed_at": observed_at, "raw_sha256": raw_sha256,
            "decision_status": "observed_only", "rejection_reasons": ["insufficient_independent_listings"],
        }
        row["observation_id"] = _observation_identity(row)
        rows.append(row)
    distinct = {row["source_id"] for row in rows}
    if len(distinct) >= 2:
        for row in rows:
            row["decision_status"] = "qualified"
            row["rejection_reasons"] = []
            row["observation_id"] = _observation_identity(row)
    unique = {row["observation_id"]: row for row in rows}
    return sorted(unique.values(), key=lambda row: (row["source_id"], row["price_cny"], row["observation_id"]))


def _redacted_payload(value: Any) -> Any:
    if isinstance(value, dict):
        return {key: _redacted_payload(child) for key, child in value.items() if key.lower() not in {"api_key", "account_email"}}
    if isinstance(value, list):
        return [_redacted_payload(child) for child in value]
    if isinstance(value, str) and value.startswith("https://"):
        parsed = urlsplit(value)
        clean = [(key, item) for key, values in parse_qs(parsed.query, keep_blank_values=True).items() if key.lower() != "api_key" for item in values]
        return urlunsplit((parsed.scheme, parsed.netloc, parsed.path, urlencode(clean), parsed.fragment))
    return value


def _checkpoint_path(paths: DataPaths) -> Path:
    return paths.runtime_root / "scheduler" / "serpapi-price-checkpoint.json"


def _load_checkpoint(paths: DataPaths) -> dict[str, Any]:
    try:
        value = json.loads(_checkpoint_path(paths).read_text(encoding="utf-8"))
        if value.get("schema_version") == 1 and isinstance(value.get("attempted_at"), dict):
            return value
    except (OSError, json.JSONDecodeError):
        pass
    return {"schema_version": 1, "attempted_at": {}, "successful_dates": []}


def collect_serpapi_baidu(
    paths: DataPaths,
    *,
    mode: str,
    scheduled_for: datetime | None = None,
    client: SerpApiClient | None = None,
) -> dict[str, Any]:
    """执行 canary 或每日微批次；canary 永不发布，daily 仅在显式激活后运行。"""
    if mode not in {"canary", "daily"}:
        raise PipelineError("serpapi_invalid_mode", "mode 仅允许 canary|daily")
    config, parts = _load_config(paths)
    if mode == "daily" and config["activation_status"] != "active":
        raise PipelineError("serpapi_not_activated", "真实 canary 尚未人工确认，价格来源未启用")
    if mode == "daily":
        registry = json.loads((paths.data_root / "sources.registry.json").read_text(encoding="utf-8"))
        registered = next((item for item in registry.get("sources", []) if item.get("id") == COLLECTOR_ID), None)
        if not registered or registered.get("enabled") is not True:
            raise PipelineError("serpapi_not_activated", "SerpApi 来源登记尚未启用")
    if client is None:
        _load_local_env(paths)
    settings = client.settings if client is not None else SerpApiSettings.from_env()
    client = client or SerpApiClient(settings)
    checkpoint = _load_checkpoint(paths)
    scheduled_for = (scheduled_for or datetime.now().astimezone()).astimezone(UTC)
    scheduled_date = scheduled_for.date().isoformat()
    all_core = [sku for values in config["core_skus"].values() for sku in values]
    idempotency_skus = list(config["canary_skus"]) if mode == "canary" else sorted(
        all_core, key=lambda sku: (checkpoint["attempted_at"].get(sku, ""), sku),
    )[: settings.daily_skus]
    run_id = str(uuid.uuid5(uuid.NAMESPACE_URL, f"pcdata-serpapi:{mode}:{scheduled_for.isoformat()}:{','.join(idempotency_skus)}"))
    if mode == "daily" and scheduled_date in checkpoint["successful_dates"]:
        return {"schema_version": 1, "run_id": run_id, "status": "no_change", "scheduled_for": scheduled_date, "calls": 0, "shopping_result_skus": 0, "dual_match_skus": 0, "model_used": False}
    if mode == "canary":
        skus = idempotency_skus
    else:
        skus = idempotency_skus
    account = client.account(len(skus))
    raw_dir = paths.runtime_root / "serpapi" / "raw" / run_id
    raw_root = paths.runtime_root / "serpapi" / "raw"
    if raw_root.is_dir():
        cutoff = time() - 7 * 24 * 60 * 60
        for path in raw_root.glob("*/*.json"):
            try:
                if path.stat().st_mtime < cutoff:
                    path.unlink()
            except OSError:
                continue
    observations: list[dict[str, Any]] = []
    hit_skus: set[str] = set()
    dual_skus: set[str] = set()
    for sku in skus:
        checkpoint["attempted_at"][sku] = _rfc3339(scheduled_for)
        _atomic_json(_checkpoint_path(paths), checkpoint)
        # Both interactive planning and this collector reserve from the same PG ledger.
        try:
            reservation = subprocess.run(
                ["go", "run", "./cmd/searchquota", "-usage", str(account["usage"]), "-budget", str(settings.monthly_budget)],
                cwd=paths.repo_root, capture_output=True, timeout=60, check=False,
            )
        except (OSError, subprocess.TimeoutExpired) as exc:
            raise PipelineError("serpapi_budget_unavailable", "共享搜索额度不可用，未发送搜索请求") from exc
        if reservation.returncode != 0:
            raise PipelineError("serpapi_budget_unavailable", "共享搜索额度不可用，未发送搜索请求")
        payload, body = client.search(_identity(config, parts[sku])["query"])
        raw_dir.mkdir(parents=True, exist_ok=True)
        (raw_dir / f"{sku}.json").write_bytes(stable_json_bytes(_redacted_payload(payload)))
        rows = _listing_rows(sku, payload, hashlib.sha256(body).hexdigest(), _identity(config, parts[sku]), _rfc3339(scheduled_for))
        if payload.get("shopping_results"):
            hit_skus.add(sku)
        if len({row["source_id"] for row in rows}) >= 2:
            dual_skus.add(sku)
        observations.extend(rows)
    normalized = paths.data_root / "work" / "price-runs" / run_id / "normalized" / "observations.jsonl"
    _write_jsonl(normalized, observations)
    report = {
        "schema_version": 1, "run_id": run_id, "status": "awaiting_manual_review" if mode == "canary" else "collected",
        "mode": mode, "scheduled_for": _rfc3339(scheduled_for), "skus": skus, "calls": len(skus),
        "shopping_result_skus": len(hit_skus), "dual_match_skus": len(dual_skus), "accepted_observations": len(observations),
        "automatic_thresholds_met": len(hit_skus) >= 10 and len(dual_skus) >= 9 if mode == "canary" else None,
        "manual_zero_mismatch_review_required": mode == "canary", "account": account, "model_used": False,
    }
    _atomic_json(paths.data_root / "work" / "price-runs" / run_id / "serpapi-report.json", report)
    if mode == "daily":
        imported = import_price_observations(paths, observations, run_id=run_id, source_file_sha256=hashlib.sha256(stable_json_bytes(report)).hexdigest())
        review = create_price_review(paths, run_id)
        publication = publish_price_review(paths, run_id, policy="automatic")
        checkpoint["successful_dates"] = sorted(set(checkpoint["successful_dates"] + [scheduled_date]))[-62:]
        report["import"] = imported
        report["review"] = review
        report["publication"] = publication
        report["status"] = publication["status"]
    _atomic_json(_checkpoint_path(paths), checkpoint)
    return report
