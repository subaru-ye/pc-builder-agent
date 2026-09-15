"""Run in an isolated image: python /tests/test_reader.py.

Real loopback HTTP cancellation and real Chromium, with offline fixtures.
The test replaces the worker file in this process only; the production API
has no fixture URL, command, configuration, or internal-network escape hatch.
"""
import asyncio
import importlib.util
import json
import os
from pathlib import Path
import socket
import sys
import unittest

import httpx
import psutil
import uvicorn

os.environ["CRAWL4AI_API_TOKEN"] = "offline-reader-test"
spec = importlib.util.spec_from_file_location("reader", "/app/pc-reader/reader.py")
reader = importlib.util.module_from_spec(spec)
spec.loader.exec_module(reader)
reader.__file__ = str(Path(__file__).with_name("browser_fixture.py"))


async def until(predicate, seconds=12):
    async with asyncio.timeout(seconds):
        while not predicate():
            await asyncio.sleep(0.05)


class ReaderLifecycle(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        reader.slots = asyncio.Semaphore(2)
        reader.MAX_SECONDS = 45
        reader.pending = 0
        reader.jobs.clear()
        reader.admissions.clear()
        sock = socket.socket()
        sock.bind(("127.0.0.1", 0))
        sock.listen()
        self.server = uvicorn.Server(uvicorn.Config(reader.app, log_level="critical", access_log=False, timeout_graceful_shutdown=1))
        self.server.install_signal_handlers = lambda: None
        self.task = asyncio.create_task(self.server.serve(sockets=[sock]))
        await until(lambda: self.server.started)
        self.client = httpx.AsyncClient(base_url=f"http://127.0.0.1:{sock.getsockname()[1]}",
                                       headers={"Authorization": "Bearer offline-reader-test"}, timeout=50)

    async def asyncTearDown(self):
        await self.client.aclose()
        self.server.should_exit = True
        await self.task
        self.assertEqual(reader.pending, 0)
        self.assertEqual(len(reader.jobs), 0)

    def start(self, path="slow"):
        return asyncio.create_task(self.client.post("/read", json={"url": "https://example.com/" + path}))

    async def browsers_started(self, count=1):
        await until(lambda: len(list(Path("/tmp").glob("pc-page-*/started.json"))) >= count)
        files = list(Path("/tmp").glob("pc-page-*/started.json"))
        pids = [pid for f in files for pid in json.loads(f.read_text())]
        self.assertTrue(any("chrom" in psutil.Process(pid).name() for pid in pids if psutil.pid_exists(pid)))
        return pids

    async def assert_reaped(self, pids):
        def live():
            result = []
            for pid in pids:
                try:
                    if psutil.Process(pid).status() != psutil.STATUS_ZOMBIE:
                        result.append(pid)
                except psutil.NoSuchProcess:
                    pass
            return result
        await until(lambda: not live())
        await until(lambda: reader.pending == 0)
        self.assertEqual(list(Path("/tmp").glob("pc-page-*")), [])

    async def test_disconnect_kills_real_browser(self):
        task = self.start()
        pids = await self.browsers_started()
        task.cancel()
        await asyncio.gather(task, return_exceptions=True)
        await self.assert_reaped(pids)
        self.assertGreater(reader.counts["cancelled"], 0)

    async def test_pending_cap_and_queue_cancellation(self):
        tasks = [self.start() for _ in range(10)]
        pids = await self.browsers_started(2)
        await until(lambda: reader.pending == 10)
        self.assertEqual(len(reader.jobs), 2)
        response = await self.client.post("/read", json={"url": "https://example.com/overflow"})
        self.assertEqual(response.status_code, 429)
        # Cancel all including queued requests; none may start an orphan job.
        for task in tasks:
            task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
        await self.assert_reaped(pids)

    async def test_deadline_reaps_browser(self):
        reader.MAX_SECONDS = 6
        task = self.start()
        pids = await self.browsers_started()
        response = await task
        self.assertEqual(response.status_code, 504)
        await self.assert_reaped(pids)

    async def test_normal_completion_and_auth(self):
        response = await self.client.post("/read", json={"url": "https://example.com/fast"}, headers={"Authorization": "wrong"})
        self.assertEqual(response.status_code, 401)
        for payload in ({"url": "http://example.com"}, {"url": "https://example.com", "js_code": "bad"}):
            response = await self.client.post("/read", json=payload)
            self.assertEqual(response.status_code, 400)
        response = await self.start("fast")
        self.assertEqual(response.status_code, 200)
        self.assertEqual(response.json()["results"][0]["markdown"]["raw_markdown"], "AM4 105W")
        self.assertEqual(list(Path("/tmp").glob("pc-page-*")), [])

    async def test_shutdown_reaps_running_browser(self):
        task = self.start()
        pids = await self.browsers_started()
        self.server.should_exit = True
        await self.task
        await asyncio.gather(task, return_exceptions=True)
        await self.assert_reaped(pids)

    async def test_rate_and_body_limits(self):
        response = await self.client.post("/read", content=b"x"*8193)
        self.assertEqual(response.status_code, 413)
        for _ in range(59):
            response = await self.client.post("/read", json={"unexpected": True})
            self.assertEqual(response.status_code, 400)
        response = await self.client.post("/read", json={"unexpected": True})
        self.assertEqual(response.status_code, 429)
        self.assertEqual(len(reader.jobs), 0)

    async def test_worker_failure_is_counted(self):
        previous = reader.counts["failed"]
        response = await self.start("fail")
        self.assertEqual(response.status_code, 502)
        self.assertEqual(reader.counts["failed"], previous+1)
        self.assertEqual(list(Path("/tmp").glob("pc-page-*")), [])


if __name__ == "__main__":
    unittest.main(verbosity=2)
