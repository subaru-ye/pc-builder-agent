import os
from pathlib import Path

ROOT = Path(__file__).resolve().parents[4]
DATA = Path(os.environ.get("COLLECTION_DATA_DIR", ROOT / "var/data/collections/2026-09-08")).resolve()
OUTPUT = Path(os.environ.get("COLLECTION_OUTPUT_DIR", DATA / "rebuild")).resolve()


def skill_dir() -> Path:
    value = os.environ.get("MAISHOU_SKILL_DIR")
    if not value:
        raise SystemExit("Set MAISHOU_SKILL_DIR to the installed taobao skill directory.")
    return Path(value).resolve()
