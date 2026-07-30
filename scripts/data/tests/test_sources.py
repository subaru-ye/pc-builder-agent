"""来源锁测试:校验锁文件结构,并断言 P1 锁定的上游版本不被悄悄改动。"""

import json

import pytest

from pcdata.canonical import SpecError
from pcdata.sources import DEFAULT_LOCK_PATH, get_source, load_lock

# 实施计划 D2/D3 冻结的上游版本:改这里必须先改实施计划与 sources.lock.json。
PINNED_PC_PART_COMMIT = "c52a04ca9465c83997ed335f7767b09a2005dd26"
PINNED_DBGPU_VERSION = "2025.12"


class TestDefaultLock:
    def test_锁文件存在且结构合法(self):
        sources = load_lock()
        assert set(sources) == {"pc-part-dataset", "dbgpu"}

    def test_pc_part_dataset锁定commit(self):
        src = get_source("pc-part-dataset")
        assert src["kind"] == "git"
        assert src["commit"] == PINNED_PC_PART_COMMIT

    def test_dbgpu锁定版本(self):
        src = get_source("dbgpu")
        assert src["kind"] == "pypi"
        assert src["version"] == PINNED_DBGPU_VERSION

    def test_未锁定来源报错(self):
        with pytest.raises(SpecError, match="未在 .* 中锁定"):
            get_source("passmark")


class TestLockValidation:
    def _write(self, tmp_path, data):
        p = tmp_path / "lock.json"
        p.write_text(json.dumps(data, ensure_ascii=False), encoding="utf-8")
        return p

    def test_schema_version必须为1(self, tmp_path):
        p = self._write(tmp_path, {"schema_version": 2, "sources": []})
        with pytest.raises(SpecError, match="schema_version"):
            load_lock(p)

    def test_git来源缺commit失败(self, tmp_path):
        p = self._write(
            tmp_path,
            {
                "schema_version": 1,
                "sources": [
                    {"name": "x", "kind": "git", "url": "https://e", "usage": "t"}
                ],
            },
        )
        with pytest.raises(SpecError, match="缺少键.*commit"):
            load_lock(p)

    def test_非法kind失败(self, tmp_path):
        p = self._write(
            tmp_path,
            {"schema_version": 1, "sources": [{"name": "x", "kind": "http"}]},
        )
        with pytest.raises(SpecError, match="非法 kind"):
            load_lock(p)

    def test_重复来源失败(self, tmp_path):
        src = {
            "name": "x",
            "kind": "pypi",
            "package": "x",
            "version": "1",
            "usage": "t",
        }
        p = self._write(tmp_path, {"schema_version": 1, "sources": [src, src]})
        with pytest.raises(SpecError, match="重复"):
            load_lock(p)


def test_默认锁路径指向scripts_data():
    assert DEFAULT_LOCK_PATH.name == "sources.lock.json"
    assert DEFAULT_LOCK_PATH.parent.name == "data"
