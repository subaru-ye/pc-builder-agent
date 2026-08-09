import assert from "node:assert/strict";
import test from "node:test";

import { isChildRunning } from "./process-state.mjs";

test("不存在的子进程允许首次启动", () => {
  assert.equal(isChildRunning(undefined), false);
});

test("仍在运行的子进程阻止重复启动", () => {
  assert.equal(isChildRunning({ exitCode: null, signalCode: null }), true);
});

test("正常退出或被信号终止的子进程允许重启", () => {
  assert.equal(isChildRunning({ exitCode: 0, signalCode: null }), false);
  assert.equal(isChildRunning({ exitCode: null, signalCode: "SIGTERM" }), false);
});
