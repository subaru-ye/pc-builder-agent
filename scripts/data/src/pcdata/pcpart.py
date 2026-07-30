"""pc-part-dataset 适配器(D2)。

只消费 sources.lock.json 锁定 commit 的八类 JSONL;丢弃美元价格;
重复(按名称查找命中多条)、单位/枚举歧义立即失败,绝不猜测。

上游各类可信字段(API.md@锁定 commit):
- cpu:tdp(W)、graphics(核显型号或 null)
- video-card:chipset、length(mm)——TDP/供电接口上游没有,走 D3 dbgpu 与人工 override
- motherboard:socket、form_factor——芯片组/内存代际/M.2 数走人工 override
- memory:speed=[DDR代际, MT/s]
- internal-hard-drive:type==SSD、form_factor(M.2-xxxx / 2.5)
- power-supply:wattage(W)——供电接口列表必须人工核验
- case / cpu-cooler:限长限高板型冷排散热字段上游缺失,仅 cooler.size 作冷排尺寸候选
"""

from __future__ import annotations

import json
import urllib.request
from pathlib import Path
from typing import Any

from .canonical import SpecError
from .sources import DEFAULT_LOCK_PATH, get_source

__all__ = [
    "CATEGORY_FILES",
    "fetch_snapshot",
    "load_raw",
    "find",
    "to_candidate",
]

# canonical 类目 -> 上游 JSONL 文件名(不含扩展名)。
CATEGORY_FILES = {
    "cpu": "cpu",
    "gpu": "video-card",
    "motherboard": "motherboard",
    "memory": "memory",
    "ssd": "internal-hard-drive",
    "psu": "power-supply",
    "case": "case",
    "cooler": "cpu-cooler",
}

_FORM_FACTOR_MAP = {"ATX": "atx", "Micro ATX": "matx", "Mini ITX": "itx"}


def fetch_snapshot(dest_dir: Path, lock_path: Path = DEFAULT_LOCK_PATH) -> list[Path]:
    """按锁定 commit 下载八类 JSONL 到本地目录(仅手工/D4 运行,CI 与测试禁用)。

    原始大文件不入库:dest_dir 应位于 .gitignore 的 upstream/ 下。
    """
    src = get_source("pc-part-dataset", lock_path)
    base = f"{src['url'].rstrip('/')}/raw/{src['commit']}/data/jsonl"
    dest_dir.mkdir(parents=True, exist_ok=True)
    written: list[Path] = []
    for filename in sorted(set(CATEGORY_FILES.values())):
        url = f"{base}/{filename}.jsonl"
        with urllib.request.urlopen(url) as resp:  # noqa: S310 - 锁定 commit 的固定 https 源
            data = resp.read()
        if not data:
            raise SpecError(f"上游文件为空: {url}")
        out = dest_dir / f"{filename}.jsonl"
        out.write_bytes(data)
        written.append(out)
    return written


def load_raw(snapshot_dir: Path, category: str) -> list[dict[str, Any]]:
    """读取一类上游 JSONL;每行必须为含非空 name 的对象。"""
    if category not in CATEGORY_FILES:
        raise SpecError(f"非法类目 {category!r}(仅 {'|'.join(CATEGORY_FILES)})")
    path = snapshot_dir / f"{CATEGORY_FILES[category]}.jsonl"
    if not path.is_file():
        raise SpecError(f"上游快照缺失 {path}(先运行 python -m pcdata.pcpart fetch)")
    records: list[dict[str, Any]] = []
    with path.open(encoding="utf-8") as f:
        for lineno, line in enumerate(f, start=1):
            line = line.strip()
            if not line:
                continue
            try:
                raw = json.loads(line)
            except json.JSONDecodeError as e:
                raise SpecError(f"{path.name}:{lineno} JSON 非法: {e}") from e
            if not isinstance(raw, dict) or not isinstance(raw.get("name"), str) or not raw["name"]:
                raise SpecError(f"{path.name}:{lineno} 缺少非空 name")
            records.append(raw)
    return records


def find(records: list[dict[str, Any]], name: str, **field_equals: Any) -> dict[str, Any]:
    """按 name(+判别字段)精确查找,必须唯一命中。

    上游 name 不唯一(同名不同容量/显存),命中多条时:若去掉美元价格字段后
    完全一致,视为同一条记录(价格本就丢弃,不构成歧义);规格字段有任何
    差异则立即失败,调用方需追加判别字段(如 capacity=2000)消歧,而不是猜。
    """
    hits = [
        r
        for r in records
        if r["name"] == name and all(r.get(k) == v for k, v in field_equals.items())
    ]
    cond = name if not field_equals else f"{name} {field_equals}"
    if not hits:
        raise SpecError(f"上游查无记录: {cond}")
    if len(hits) > 1:
        stripped = [
            {k: v for k, v in r.items() if k not in ("price", "price_per_gb")} for r in hits
        ]
        if any(s != stripped[0] for s in stripped[1:]):
            raise SpecError(f"上游记录重复({len(hits)} 条): {cond},请追加判别字段")
    return hits[0]


def _int_exact(field: str, v: Any) -> int:
    """mm/W/MT/s 必须是精确整数;小数即单位歧义,立即失败。"""
    if isinstance(v, bool) or not isinstance(v, (int, float)):
        raise SpecError(f"字段 {field} 必须为数值,得到 {v!r}")
    if isinstance(v, float):
        if not v.is_integer():
            raise SpecError(f"字段 {field} 存在单位歧义(非整数 {v!r})")
        v = int(v)
    if v <= 0:
        raise SpecError(f"字段 {field} 必须为正数,得到 {v}")
    return v


def _cand_cpu(raw: dict[str, Any]) -> dict[str, Any]:
    graphics = raw.get("graphics")
    if graphics is not None and (not isinstance(graphics, str) or not graphics):
        raise SpecError(f"cpu graphics 字段歧义: {graphics!r}")
    out: dict[str, Any] = {"has_igpu": graphics is not None}
    if raw.get("tdp") is not None:
        out["tdp_w"] = _int_exact("tdp", raw["tdp"])
    return out


def _cand_gpu(raw: dict[str, Any]) -> dict[str, Any]:
    out: dict[str, Any] = {}
    chipset = raw.get("chipset")
    if chipset is not None:
        if not isinstance(chipset, str) or not chipset:
            raise SpecError(f"gpu chipset 字段歧义: {chipset!r}")
        out["chipset"] = chipset  # 供 D3 与 dbgpu 显式映射,不进 canonical specs
    if raw.get("length") is not None:
        out["length_mm"] = _int_exact("length", raw["length"])
    return out


def _cand_motherboard(raw: dict[str, Any]) -> dict[str, Any]:
    out: dict[str, Any] = {}
    socket = raw.get("socket")
    if socket is not None:
        if not isinstance(socket, str) or not socket:
            raise SpecError(f"motherboard socket 字段歧义: {socket!r}")
        out["socket"] = socket
    ff = raw.get("form_factor")
    if ff is not None:
        if ff not in _FORM_FACTOR_MAP:
            raise SpecError(
                f"motherboard form_factor {ff!r} 无法映射(仅 {'/'.join(_FORM_FACTOR_MAP)});"
                "P1 目录只收 atx|matx|itx"
            )
        out["form_factor"] = _FORM_FACTOR_MAP[ff]
    return out


def _cand_memory(raw: dict[str, Any]) -> dict[str, Any]:
    speed = raw.get("speed")
    if speed is None:
        return {}
    if (
        not isinstance(speed, list)
        or len(speed) != 2
        or isinstance(speed[0], bool)
        or not isinstance(speed[0], int)
    ):
        raise SpecError(f"memory speed 字段歧义: {speed!r}")
    gen, mts = speed
    if gen not in (2, 3, 4, 5):
        raise SpecError(f"memory DDR 代际无法识别: {gen!r}")
    return {"generation": f"ddr{gen}", "speed_mts": _int_exact("speed[1]", mts)}


def _cand_ssd(raw: dict[str, Any]) -> dict[str, Any]:
    if raw.get("type") != "SSD":
        raise SpecError(f"仅收录 SSD,得到 type={raw.get('type')!r}")
    ff = raw.get("form_factor")
    if ff is None:
        return {}
    if isinstance(ff, str) and ff.startswith("M.2"):
        return {"form_factor": "m2"}
    if ff == 2.5 or ff == "2.5" or ff == '2.5"':
        return {"form_factor": "sata_2_5"}
    raise SpecError(f"ssd form_factor {ff!r} 无法映射(仅 M.2-xxxx / 2.5)")


def _cand_psu(raw: dict[str, Any]) -> dict[str, Any]:
    if raw.get("wattage") is None:
        return {}
    return {"wattage_w": _int_exact("wattage", raw["wattage"])}


def _cand_case(raw: dict[str, Any]) -> dict[str, Any]:
    # 限长/限高/支持板型/冷排上游缺失,全部走人工 override(D4d)。
    return {}


def _cand_cooler(raw: dict[str, Any]) -> dict[str, Any]:
    # type/height/解热能力上游缺失;size 仅作 AIO 冷排尺寸候选。
    if raw.get("size") is None:
        return {}
    return {"radiator_size_mm": _int_exact("size", raw["size"])}


_CANDIDATE_FUNCS = {
    "cpu": _cand_cpu,
    "gpu": _cand_gpu,
    "motherboard": _cand_motherboard,
    "memory": _cand_memory,
    "ssd": _cand_ssd,
    "psu": _cand_psu,
    "case": _cand_case,
    "cooler": _cand_cooler,
}


def to_candidate(category: str, raw: dict[str, Any]) -> dict[str, Any]:
    """把一条上游记录转成 canonical 字段候选(部分字段,不含价格)。

    输出只含上游可信字段;缺的字段由 D3 合并器按
    人工 override > pc-part 产品字段 > dbgpu 芯片字段 > null 决定。
    """
    if category not in _CANDIDATE_FUNCS:
        raise SpecError(f"非法类目 {category!r}(仅 {'|'.join(_CANDIDATE_FUNCS)})")
    candidate = _CANDIDATE_FUNCS[category](raw)
    assert "price" not in candidate  # 美元价格必须丢弃
    return candidate


def _main(argv: list[str]) -> int:
    import sys

    if len(argv) >= 2 and argv[1] == "fetch":
        dest = Path(argv[2]) if len(argv) > 2 else Path(__file__).resolve().parents[2] / "upstream"
        written = fetch_snapshot(dest)
        for p in written:
            print(f"已下载 {p}")
        return 0
    print("用法: python -m pcdata.pcpart fetch [dest_dir]", file=sys.stderr)
    return 2


if __name__ == "__main__":
    import sys

    sys.exit(_main(sys.argv))
