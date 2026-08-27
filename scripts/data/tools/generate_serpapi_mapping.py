"""显式重建 96 SKU SerpApi 查询映射；运行后必须人工审查 Git diff。"""

from __future__ import annotations

import json

from pcdata.automation import DataPaths
from pcdata.coverage import load_parts_jsonl
from pcdata.serpapi_baidu import _derived_identity


def main() -> None:
    paths = DataPaths.defaults()
    target = paths.data_root / "serpapi-baidu-products.json"
    config = json.loads(target.read_text(encoding="utf-8"))
    parts = {}
    for source in sorted(paths.seed_parts.glob("*.jsonl")):
        for record in load_parts_jsonl(source):
            parts[record["sku"]] = record
    skus = [sku for values in config["core_skus"].values() for sku in values]
    config["products"] = {sku: _derived_identity(config, parts[sku]) for sku in sorted(skus)}
    target.write_text(json.dumps(config, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")


if __name__ == "__main__":
    main()
