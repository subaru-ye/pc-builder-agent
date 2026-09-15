"""Offline browser lifecycle fixture, used only by test_reader.py.

Uses actual Chromium and a locally fulfilled HTTPS document; never fetches the
network and never changes the production reader's URL or egress checks.
"""
import asyncio
import json
import os
from pathlib import Path
import sys

from playwright.async_api import async_playwright
import psutil


async def main():
    directory = Path(sys.argv[2])
    url = json.loads((directory / "input.json").read_text())["url"]
    async with async_playwright() as playwright:
        browser = await playwright.chromium.launch(headless=True, args=["--no-sandbox"])
        page = await browser.new_page()
        await page.route("**/*", lambda route: route.fulfill(body="<h1>Offline specification</h1><p>AM4 105W</p>", content_type="text/html"))
        await page.goto("https://example.com/spec")
        pids = [os.getpid()] + [p.pid for p in psutil.Process().children(recursive=True)]
        (directory / "started.json").write_text(json.dumps(pids))
        if url.endswith("/fail"):
            raise RuntimeError("offline worker failure")
        if not url.endswith("/fast"):
            await asyncio.sleep(120)
        (directory / "result.json").write_text(json.dumps({"success": True, "results": [{
            "success": True, "status_code": 200, "final_status": 200,
            "final_url": url, "metadata": {"title": "Offline specification"},
            "markdown": {"raw_markdown": "AM4 105W"}}]}))
        await browser.close()


asyncio.run(main())
