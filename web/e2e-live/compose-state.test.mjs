import assert from "node:assert/strict";
import test from "node:test";
import { assertComposeServices, parseComposePS } from "./compose-state.mjs";

test("parseComposePS supports JSON lines without container names", () => {
  const rows = parseComposePS([
    JSON.stringify({ Service: "postgres", State: "running", Health: "healthy", Name: "generated-postgres-1" }),
    JSON.stringify({ Service: "redis", State: "running", Health: "healthy", Name: "generated-redis-1" }),
  ].join("\n"));
  assert.equal(rows.length, 2);
  assert.doesNotThrow(() => assertComposeServices(rows));
});

test("parseComposePS supports array output", () => {
  const rows = parseComposePS(JSON.stringify([
    { Service: "postgres", State: "running", Health: "healthy" },
    { Service: "redis", State: "running", Health: "healthy" },
  ]));
  assert.doesNotThrow(() => assertComposeServices(rows));
});

test("assertComposeServices rejects missing or unhealthy service", () => {
  assert.throws(() => assertComposeServices([{ Service: "postgres", State: "running", Health: "healthy" }]), /redis/);
  assert.throws(() => assertComposeServices([
    { Service: "postgres", State: "running", Health: "starting" },
    { Service: "redis", State: "running", Health: "healthy" },
  ]), /health=starting/);
});
