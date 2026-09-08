# -*- coding: utf-8 -*-
"""Minimal MCP SSE client for manmanbuy searchZheKou tool."""
import os
from batch_paths import DATA
import json
import queue
import sys
import threading
import time
import urllib.request
import urllib.parse

BASE = "https://mpc.manmanbuy.com/sse"
TOKEN = os.environ.get("MMB_API_KEY", "")

KEYWORDS = [
    "i3 12100F",
    "i5 12400F",
    "锐龙5 5500",
    "锐龙5 5600",
    "锐龙5 7500F",
    "微星 PRO H610M-G DDR4",
    "微星 PRO A620M-E",
    "微星 B550M PRO-VDH WIFI",
    "金士顿 FURY 野兽 DDR4 3200 8G×2",
    "海盗船 LPX DDR4 3200 8G×2",
    "金士顿 A400 480G",
    "金士顿 NV2 1T",
    "英睿达 P3 Plus 1T",
    "微星 MAG A650BN",
    "海盗船 CX650M",
    "华硕 PRIME AP201",
    "分形工艺 Pop Air",
    "利民 PA120 SE",
    "九州风神 AG400",
]

events = queue.Queue()


def sse_reader(resp):
    event_name = None
    data_buf = []
    for raw in resp:
        line = raw.decode("utf-8", "replace").rstrip("\r\n")
        if line == "":
            if data_buf:
                events.put((event_name or "message", "\n".join(data_buf)))
            event_name = None
            data_buf = []
            continue
        if line.startswith("event:"):
            event_name = line.split(":", 1)[1].strip()
        elif line.startswith("data:"):
            data_buf.append(line.split(":", 1)[1].lstrip(" "))
    events.put(("__closed__", ""))


def post_json(url, payload):
    req = urllib.request.Request(
        url,
        data=json.dumps(payload).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=30) as r:
        return r.status, r.read().decode("utf-8", "replace")


def wait_response(req_id, timeout=60):
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            name, data = events.get(timeout=1)
        except queue.Empty:
            continue
        if name == "__closed__":
            raise RuntimeError("SSE stream closed")
        if name in ("message", "endpoint"):
            try:
                msg = json.loads(data)
            except Exception:
                continue
            if msg.get("id") == req_id:
                return msg
    raise TimeoutError(f"no response for id={req_id}")


def main():
    if not TOKEN:
        raise SystemExit("Set MMB_API_KEY before requesting manmanbuy.")
    DATA.mkdir(parents=True, exist_ok=True)
    url = f"{BASE}?token={urllib.parse.quote(TOKEN)}"
    req = urllib.request.Request(url, headers={"Accept": "text/event-stream"})
    resp = urllib.request.urlopen(req, timeout=120)
    t = threading.Thread(target=sse_reader, args=(resp,), daemon=True)
    t.start()

    # wait for endpoint event
    deadline = time.time() + 30
    msg_url = None
    while time.time() < deadline:
        try:
            name, data = events.get(timeout=1)
        except queue.Empty:
            continue
        if name == "endpoint":
            msg_url = urllib.parse.urljoin(BASE, data.strip())
            break
    if not msg_url:
        print(json.dumps({"error": "no endpoint event received"}))
        return
    sys.stderr.write("SSE endpoint received\n")

    init = {
        "jsonrpc": "2.0",
        "id": 1,
        "method": "initialize",
        "params": {
            "protocolVersion": "2024-11-05",
            "capabilities": {},
            "clientInfo": {"name": "wb-client", "version": "1.0"},
        },
    }
    post_json(msg_url, init)
    init_resp = wait_response(1)
    server_info = init_resp.get("result", {}).get("serverInfo", {})
    sys.stderr.write(f"SERVER={json.dumps(server_info, ensure_ascii=False)}\n")

    post_json(msg_url, {"jsonrpc": "2.0", "method": "notifications/initialized"})

    out = {}
    req_id = 10
    for kw in KEYWORDS:
        payload = {
            "jsonrpc": "2.0",
            "id": req_id,
            "method": "tools/call",
            "params": {"name": "searchZheKou", "arguments": {"keyword": kw}},
        }
        try:
            post_json(msg_url, payload)
            resp_msg = wait_response(req_id, timeout=60)
            if "error" in resp_msg:
                out[kw] = {"error": resp_msg["error"]}
            else:
                content = resp_msg.get("result", {}).get("content", [])
                text = "\n".join(
                    c.get("text", "") for c in content if c.get("type") == "text"
                )
                out[kw] = text
        except Exception as e:
            out[kw] = {"error": str(e)}
        req_id += 1
        time.sleep(2)

    with open(DATA / "mmb_results.json", "w", encoding="utf-8") as f:
        json.dump(out, f, ensure_ascii=False, indent=2)
    print("DONE")


if __name__ == "__main__":
    main()
