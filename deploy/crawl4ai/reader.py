"""Bounded, anonymous Crawl4AI HTTP reader. No caller-supplied crawl config.

Each job owns a process group and temporary browser state. Disconnect, deadline,
normal completion and shutdown all reap that group before releasing capacity.
This trades browser startup time for a verifiable resource/cookie boundary.
"""
import asyncio
from collections import deque
from contextlib import asynccontextmanager
import hmac
import json
import os
from pathlib import Path
import signal
import sys
import tempfile
import time
from urllib.parse import urlsplit

from fastapi import FastAPI, HTTPException, Request

MAX_ACTIVE = 2
MAX_PENDING = 10  # two running + eight waiting, on the actual /read path
MAX_SECONDS = 45
MAX_OUTPUT = 3 * 1024 * 1024
TOKEN = os.environ.get("CRAWL4AI_API_TOKEN", "")
slots = asyncio.Semaphore(MAX_ACTIVE)
pending = 0
jobs = set()
admissions = deque()
counts = {"completed": 0, "cancelled": 0, "timed_out": 0, "rejected": 0, "failed": 0}


def https_url(url):
    try:
        value = urlsplit(url)
        if (value.scheme != "https" or not value.hostname or value.username is not None
                or value.password is not None or value.port not in (None, 443)):
            raise ValueError()
    except (ValueError, TypeError):
        raise ValueError("public HTTPS URL required") from None
    return url


async def reap(process):
    # A child can exit before Chromium, so kill the group even after wait().
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    await process.wait()


@asynccontextmanager
async def lifespan(_app):
    if not TOKEN:
        raise RuntimeError("CRAWL4AI_API_TOKEN is required")
    yield
    await asyncio.gather(*(reap(p) for p in tuple(jobs)))


app = FastAPI(lifespan=lifespan, docs_url=None, redoc_url=None, openapi_url=None)


@app.get("/health")
async def health():
    return {"status": "ok", "protocol": "pc-page-reader/1"}


def authorize(request):
    if not TOKEN or not hmac.compare_digest(request.headers.get("authorization", ""), "Bearer " + TOKEN):
        raise HTTPException(401, "unauthorized")


@app.get("/metrics")
async def metrics(request: Request):
    authorize(request)
    return {"pending": pending, "active": len(jobs), "max_active": MAX_ACTIVE,
            "max_pending": MAX_PENDING, "counts": counts,
            "browser": {"headless": True, "text_mode": True, "persistent": False}}


async def disconnected(request):
    while not await request.is_disconnected():
        await asyncio.sleep(0.05)


async def execute(url):
    async with slots:
        with tempfile.TemporaryDirectory(prefix="pc-page-") as temporary:
            root = Path(temporary)
            (root / "input.json").write_text(json.dumps({"url": url}), encoding="utf-8")
            spawning = asyncio.create_task(asyncio.create_subprocess_exec(
                sys.executable, str(Path(__file__).resolve()), "--worker", temporary,
                start_new_session=True, stdout=asyncio.subprocess.DEVNULL,
                stderr=asyncio.subprocess.DEVNULL,
            ))
            try:
                process = await asyncio.shield(spawning)
            except asyncio.CancelledError:
                process = await spawning
                await reap(process)
                raise
            jobs.add(process)
            try:
                await process.wait()
                output = root / "result.json"
                if process.returncode != 0 or not output.exists() or output.stat().st_size > MAX_OUTPUT:
                    raise HTTPException(502, "reader failed or response too large")
                return json.loads(output.read_text(encoding="utf-8"))
            finally:
                await reap(process)
                jobs.discard(process)


@app.post("/read")
async def read(request: Request):
    global pending
    authorize(request)
    now = time.monotonic()
    while admissions and admissions[0] <= now-60:
        admissions.popleft()
    if len(admissions) >= 60:
        counts["rejected"] += 1
        raise HTTPException(429, "reader rate limit reached")
    if pending >= MAX_PENDING:
        counts["rejected"] += 1
        raise HTTPException(429, "reader capacity reached; use existing evidence")
    admissions.append(now)
    pending += 1
    work = watcher = None
    started = time.monotonic()
    try:
        body = bytearray()
        async with asyncio.timeout(2):
            async for chunk in request.stream():
                body.extend(chunk)
                if len(body) > 8192:
                    raise HTTPException(413, "request too large")
        try:
            payload = json.loads(body)
            if not isinstance(payload, dict) or set(payload) != {"url"} or not isinstance(payload["url"], str):
                raise ValueError()
            url = https_url(payload["url"])
        except (ValueError, TypeError):
            raise HTTPException(400, "only one public HTTPS url is accepted") from None
        work = asyncio.create_task(execute(url))
        watcher = asyncio.create_task(disconnected(request))
        done, _ = await asyncio.wait((work, watcher), timeout=max(0, MAX_SECONDS-(time.monotonic()-started)),
                                     return_when=asyncio.FIRST_COMPLETED)
        if watcher in done:
            counts["cancelled"] += 1
            raise HTTPException(499, "request cancelled")
        if work not in done:
            counts["timed_out"] += 1
            raise HTTPException(504, "reader deadline exceeded")
        result = work.result()
        counts["completed"] += 1
        return result
    except TimeoutError:
        counts["timed_out"] += 1
        raise HTTPException(408, "request body timeout") from None
    except HTTPException as error:
        if error.status_code >= 500 and error.status_code != 504:
            counts["failed"] += 1
        raise
    except Exception:
        counts["failed"] += 1
        raise HTTPException(502, "reader unavailable") from None
    finally:
        tasks = [t for t in (work, watcher) if t is not None]
        for task in tasks:
            if not task.done():
                task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
        pending -= 1


async def crawl_worker(directory):
    # Keep cache/temp state inside the parent-owned directory, deleted on every
    # exit. The installed browser executable cache stays read-only and shared.
    os.environ["CRAWL4AI_BASE_DIRECTORY"] = directory
    os.environ["TMPDIR"] = directory
    sys.path.insert(0, "/app")
    from crawl4ai import AsyncWebCrawler, BrowserConfig, CrawlerRunConfig, CacheMode
    from egress_broker import enforce_egress, set_egress_proxy, resolve_and_pin
    from egress_proxy import PinningProxy

    url = https_url(json.loads((Path(directory) / "input.json").read_text())["url"])
    resolve_and_pin(url)
    proxy = PinningProxy()
    await proxy.start()
    set_egress_proxy(proxy.url)
    browser = BrowserConfig(headless=True, text_mode=True, verbose=False,
                            use_persistent_context=False,
                            extra_args=["--disable-dev-shm-usage", "--disable-gpu"])
    enforce_egress(browser)  # pinned public-IP dialer, TLS validation, no proxy bypass
    state = {"final_url": url, "final_status": 0}

    async def on_page(page, context, **kwargs):
        async def guard(route):
            try:
                https_url(route.request.url)
            except ValueError:
                await route.abort()
                return
            await route.fallback()

        # Guard all requests including redirect transitions; the egress proxy
        # separately pins the actual resolved IP, avoiding a DNS-check/dial gap.
        await page.route("**/*", guard)

        def response_seen(response):
            if response.request.is_navigation_request() and response.frame == page.main_frame:
                state["final_status"] = response.status
                state["final_url"] = response.url
        page.on("response", response_seen)
        return page

    try:
        async with AsyncWebCrawler(config=browser) as crawler:
            crawler.crawler_strategy.set_hook("on_page_context_created", on_page)
            result = await crawler.arun(url, config=CrawlerRunConfig(
                cache_mode=CacheMode.BYPASS, page_timeout=35000, wait_until="domcontentloaded",
                delay_before_return_html=0.5, word_count_threshold=0,
                excluded_tags=["nav", "footer"], verbose=False,
            ))
            text = result.markdown.raw_markdown if result.markdown else ""
            if len(text.encode("utf-8")) > 2 * 1024 * 1024:
                raise ValueError("body too large")
            observed = bool(result.success and state["final_status"])
            row = {"success": observed, "status_code": result.status_code,
                   **state, "markdown": {"raw_markdown": text}, "metadata": result.metadata or {}}
            (Path(directory) / "result.json").write_text(
                json.dumps({"success": observed, "results": [row]}, ensure_ascii=False), encoding="utf-8")
    finally:
        await proxy.stop()


if __name__ == "__main__":
    if len(sys.argv) == 3 and sys.argv[1] == "--worker":
        asyncio.run(crawl_worker(sys.argv[2]))
    else:
        import uvicorn
        uvicorn.run(app, host="0.0.0.0", port=11235, access_log=False, log_level="warning", timeout_graceful_shutdown=3)
