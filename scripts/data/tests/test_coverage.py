"""完整率报告测试:只使用 tests/fixtures 冻结数据,不做网络与 DB 访问。"""

from pathlib import Path

import pytest

from pcdata.canonical import SpecError
from pcdata.coverage import (
    EXPECTED_PER_CATEGORY,
    build_report,
    load_parts_jsonl,
    load_priced_skus,
)

FIXTURES = Path(__file__).parent / "fixtures"


@pytest.fixture(scope="module")
def sample_parts():
    return load_parts_jsonl(FIXTURES / "parts_sample.jsonl")


class TestLoadPartsJSONL:
    def test_fixture加载并校验(self, sample_parts):
        assert len(sample_parts) == 9
        assert sample_parts[0]["sku"] == "cpu-r5-7600"

    def test_重复sku失败(self, tmp_path, sample_parts):
        import json

        p = tmp_path / "dup.jsonl"
        line = json.dumps(sample_parts[0], ensure_ascii=False)
        p.write_text(line + "\n" + line + "\n", encoding="utf-8")
        with pytest.raises(SpecError, match="重复 SKU"):
            load_parts_jsonl(p)

    def test_非法记录带行号失败(self, tmp_path):
        p = tmp_path / "bad.jsonl"
        p.write_text('{"sku": "x"}\n', encoding="utf-8")
        with pytest.raises(SpecError, match="bad.jsonl:1"):
            load_parts_jsonl(p)


class TestBuildReport:
    def test_每类计数与20目标(self, sample_parts):
        report = build_report(sample_parts)
        assert report["total_skus"] == 9
        assert report["per_category"]["cpu"]["sku_count"] == 1
        assert report["per_category"]["cooler"]["sku_count"] == 2
        assert report["per_category"]["cpu"]["expected"] == EXPECTED_PER_CATEGORY
        # 数量不足 20,整体门禁不通过。
        assert not report["per_category"]["cpu"]["count_ok"]
        assert not report["catalog_ok"]

    def test_错误级字段无缺口(self, sample_parts):
        report = build_report(sample_parts)
        for category, stats in report["per_category"].items():
            assert stats["error_fields_ok"], f"{category} 存在错误级字段缺口"
            assert stats["error_field_gaps"] == {}

    def test_warning字段null被统计(self, sample_parts):
        report = build_report(sample_parts)
        cooler = report["per_category"]["cooler"]
        # aio 散热器 cooling_capacity_w 为 null:允许,但必须出现在统计里。
        assert cooler["warning_field_null_skus"]["cooling_capacity_w"] == ["cooler-ls520"]
        mb = report["per_category"]["motherboard"]
        assert mb["warning_field_null_skus"]["memory_speed_max_mts"] == []

    def test_仅纯itx机箱统计电源安装字段(self, sample_parts):
        parts = [dict(p, specs=dict(p["specs"])) for p in sample_parts]
        case = next(p for p in parts if p["category"] == "case")
        case["specs"]["supported_psu_form_factors"] = None
        case["specs"]["psu_length_max_mm"] = None

        case["specs"]["supported_form_factors"] = ["atx", "matx", "itx"]
        report = build_report(parts)
        warning = report["per_category"]["case"]["warning_field_null_skus"]
        assert warning["supported_psu_form_factors"] == []
        assert warning["psu_length_max_mm"] == []

        case["specs"]["supported_form_factors"] = ["itx"]
        report = build_report(parts)
        warning = report["per_category"]["case"]["warning_field_null_skus"]
        assert warning["supported_psu_form_factors"] == [case["sku"]]
        assert warning["psu_length_max_mm"] == [case["sku"]]

    def test_错误级字段缺口被点名(self, sample_parts):
        broken = [dict(p, specs=dict(p["specs"])) for p in sample_parts]
        broken[0]["specs"]["socket"] = None  # cpu-r5-7600
        report = build_report(broken)
        cpu = report["per_category"]["cpu"]
        assert not cpu["error_fields_ok"]
        assert cpu["error_field_gaps"]["socket"] == ["cpu-r5-7600"]
        assert not report["catalog_ok"]

    def test_aio条件字段缺口(self, sample_parts):
        broken = [dict(p, specs=dict(p["specs"])) for p in sample_parts]
        aio = next(p for p in broken if p["sku"] == "cooler-ls520")
        aio["specs"]["radiator_size_mm"] = None
        report = build_report(broken)
        gaps = report["per_category"]["cooler"]["error_field_gaps"]
        assert gaps["radiator_size_mm"] == ["cooler-ls520"]

    def test_价格全覆盖(self, sample_parts):
        skus = {p["sku"] for p in sample_parts}
        report = build_report(sample_parts, priced_skus=skus)
        assert report["price"]["price_ok"]
        assert report["price"]["covered"] == report["price"]["total"] == 9

    def test_价格缺口与目录外sku(self, sample_parts):
        skus = {p["sku"] for p in sample_parts} - {"psu-750w"} | {"sku-ghost"}
        report = build_report(sample_parts, priced_skus=skus)
        assert report["price"]["missing_skus"] == ["psu-750w"]
        assert report["price"]["unknown_priced_skus"] == ["sku-ghost"]
        assert not report["price"]["price_ok"]
        assert not report["catalog_ok"]


class TestLoadPricedSKUs:
    def test_固定表头与去重(self, tmp_path):
        p = tmp_path / "prices.csv"
        p.write_text(
            "sku,price_cny,source,captured_at\n"
            "cpu-r5-7600,1299.00,jd,2026-07-27\n"
            "gpu-rtx4070,4599.00,jd,2026-07-27\n",
            encoding="utf-8",
        )
        assert load_priced_skus(p) == {"cpu-r5-7600", "gpu-rtx4070"}

    def test_表头不符失败(self, tmp_path):
        p = tmp_path / "bad.csv"
        p.write_text("sku,price_usd\nx,1\n", encoding="utf-8")
        with pytest.raises(SpecError, match="表头必须为"):
            load_priced_skus(p)

    def test_重复sku失败(self, tmp_path):
        p = tmp_path / "dup.csv"
        p.write_text(
            "sku,price_cny,source,captured_at\n"
            "a,1.00,jd,2026-07-27\n"
            "a,2.00,jd,2026-07-27\n",
            encoding="utf-8",
        )
        with pytest.raises(SpecError, match="重复 SKU"):
            load_priced_skus(p)


class TestOpenScale:
    """扩库数量开放:>=20 下限、各类不等、可超 160;身份与非法记录校验不放宽。"""

    @staticmethod
    def _expanded(sample_parts, per_cat=21):
        parts = []
        for category in [
            "cpu", "gpu", "motherboard", "memory", "ssd", "psu", "case", "cooler",
        ]:
            template = next(p for p in sample_parts if p["category"] == category)
            # cooler 多 3 条,保证各类数量不等。
            n = per_cat + 3 if category == "cooler" else per_cat
            for i in range(n):
                parts.append({
                    **template,
                    "sku": f"{category}-scale-{i:03d}",
                    "model": f"{template['model']} scale {i}",
                })
        return parts

    def test_超160条且各类不等全部通过(self, sample_parts):
        parts = self._expanded(sample_parts)
        assert len(parts) > 160
        report = build_report(parts, priced_skus={p["sku"] for p in parts})
        assert report["total_skus"] == len(parts)
        for category, stats in report["per_category"].items():
            assert stats["count_ok"], category
        assert (
            report["per_category"]["cooler"]["sku_count"]
            > report["per_category"]["cpu"]["sku_count"]
        )
        assert report["price"]["price_ok"]
        assert report["catalog_ok"]

    def test_低于下限的类仍失败(self, sample_parts):
        parts = [p for p in self._expanded(sample_parts) if p["category"] != "gpu"]
        report = build_report(parts)
        assert not report["per_category"]["gpu"]["count_ok"]
        assert not report["catalog_ok"]
        assert report["per_category"]["cpu"]["count_ok"]

    def test_扩容后重复sku仍拒绝(self, sample_parts, tmp_path):
        import json

        lines = [json.dumps(p, ensure_ascii=False) for p in self._expanded(sample_parts)]
        p = tmp_path / "big.jsonl"
        p.write_text("\n".join(lines) + "\n" + lines[0] + "\n", encoding="utf-8")
        with pytest.raises(SpecError, match="重复 SKU"):
            load_parts_jsonl(p)

    def test_扩容后非法记录仍拒绝(self, sample_parts, tmp_path):
        import json

        lines = [json.dumps(p, ensure_ascii=False) for p in self._expanded(sample_parts)]
        p = tmp_path / "big.jsonl"
        p.write_text("\n".join(lines) + "\n" + '{"sku": "x"}\n', encoding="utf-8")
        with pytest.raises(SpecError, match="big.jsonl"):
            load_parts_jsonl(p)
