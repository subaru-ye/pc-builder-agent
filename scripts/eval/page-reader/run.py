"""Isolated live recheck; 24 reads max, no search/model calls, no shared restart."""
from pathlib import Path
import argparse
import hashlib
import json
import os
import secrets
import subprocess
import time
import urllib.request

root = Path(__file__).resolve().parents[3]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--dataset", required=True)
    parser.add_argument("--out", required=True)
    parser.add_argument("--max-reads", type=int, default=24)
    args = parser.parse_args()
    if Path(args.out).exists() or not 1 <= args.max_reads <= 24:
        raise SystemExit("new output directory and max-reads 1..24 required")
    name = "pcbuilder-reader-recheck-20260914"
    env = dict(os.environ, CRAWL4AI_API_TOKEN=secrets.token_hex(32), CRAWL4AI_URL="http://127.0.0.1:11336")
    # Token is inherited by Docker from the environment, never put in argv/logs.
    subprocess.run(["docker", "run", "-d", "--init", "--name", name, "-p", "127.0.0.1:11336:11235",
                    "--shm-size", "1g", "--memory", "4g", "--cpus", "2", "--entrypoint", "python",
                    "-e", "CRAWL4AI_API_TOKEN", "-e", "CRAWL4AI_ALLOW_INTERNAL_URLS=false",
                    "-e", "CRAWL4AI_ALLOW_INSECURE_TLS=false",
                    "-v", str(root / "deploy/crawl4ai/reader.py")+":/app/pc-reader/reader.py:ro",
                    "unclecode/crawl4ai:0.9.3", "/app/pc-reader/reader.py"], check=True, env=env, stdout=subprocess.DEVNULL)
    try:
        for _ in range(60):
            try:
                with urllib.request.urlopen(env["CRAWL4AI_URL"]+"/health", timeout=1) as response:
                    assert json.load(response)["protocol"] == "pc-page-reader/1"
                break
            except Exception:
                time.sleep(0.5)
        else:
            raise RuntimeError("isolated reader failed to start")
        subprocess.run(["go", "run", "./scripts/eval/page-reader", "-dataset", args.dataset,
                        "-out", args.out, "-max-reads", str(args.max_reads)], cwd=root, env=env, check=True)
        request = urllib.request.Request(env["CRAWL4AI_URL"]+"/metrics", headers={"Authorization": "Bearer "+env["CRAWL4AI_API_TOKEN"]})
        with urllib.request.urlopen(request, timeout=3) as response:
            (Path(args.out) / "service-metrics.json").write_bytes(response.read())
        stats = subprocess.check_output(["docker", "stats", "--no-stream", "--format", "{{json .}}", name])
        (Path(args.out) / "final-container-stats.json").write_bytes(stats)
        resource = subprocess.check_output(["docker", "exec", name, "python", "-c",
            "import json,pathlib; print(json.dumps({p:pathlib.Path('/sys/fs/cgroup/'+p).read_text() for p in ['memory.current','memory.peak','cpu.stat','pids.current']}))"])
        (Path(args.out) / "cgroup-final.json").write_bytes(resource)
        files = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in Path(args.out).glob("*.json")}
        (Path(args.out) / "manifest.json").write_text(json.dumps(files, indent=2), encoding="utf-8")
    finally:
        subprocess.run(["docker", "rm", "-f", name], check=True, stdout=subprocess.DEVNULL)


if __name__ == "__main__":
    main()
