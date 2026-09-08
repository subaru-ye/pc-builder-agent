from batch_paths import DATA, OUTPUT
import json
from pathlib import Path

LINKS = DATA / "accept_links_20260908.json"
JSONL = OUTPUT / "learning-price-rows-full-20260908.jsonl"

links = json.loads(LINKS.read_text(encoding="utf-8"))
rows = [json.loads(l) for l in JSONL.read_text(encoding="utf-8").splitlines() if l.strip()]

merged = mismatch = nolink = 0
for o in rows:
    if o["decision"] != "accept":
        continue
    info = links.get(o["target"], {})
    url = info.get("url")
    if url:
        o["listing_url"] = url
        merged += 1
        dp = info.get("detail_price")
        if dp is not None:
            try:
                if abs(float(dp) - float(o["price_cny"])) > 0.005:
                    mismatch += 1
                    o.setdefault("flags", []).append("listing_url_price_drift")
                    o["flags"] = sorted(set(o["flags"]))
                    print(f"PRICE-DRIFT {o['target']}: accept={o['price_cny']} detail={dp}")
            except ValueError:
                pass
    else:
        nolink += 1
        print(f"NO-LINK {o['target']}: {info.get('error')}")

JSONL.write_text(
    "\n".join(json.dumps(o, ensure_ascii=False) for o in rows) + "\n", encoding="utf-8"
)
print(f"merged listing_url into {merged} accept rows; {nolink} without link; {mismatch} price drifts")
