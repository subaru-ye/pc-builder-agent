import { decodeRunEvent, SSEParser } from "./sse";
import { describe, expect, it } from "vitest";

describe("SSEParser", () => {
  it("handles split chunks, comments and multiline data", () => {
    const frames: Parameters<typeof decodeRunEvent>[0][] = [];
    const parser = new SSEParser((frame) => frames.push(frame));
    parser.feed(": heartbeat\n\nid: 1-0\nevent: run.progress\ndata: {\"schema_version\":1,");
    parser.feed("\ndata: \"run_id\":\"run-1\",\"timestamp\":\"2026-08-09T10:00:00Z\",\"payload\":{\"stage\":\"screening\"}}\n\n");
    expect(frames[0]).toEqual({ heartbeat: true });
    expect(decodeRunEvent(frames[1])?.event).toBe("run.progress");
    expect(decodeRunEvent(frames[1])?.data.payload.stage).toBe("screening");
  });

  it("rejects internal or malformed events", () => {
    expect(decodeRunEvent({ id: "1", event: "a2a.internal", data: "{}" })).toBeNull();
    expect(decodeRunEvent({ event: "run.completed", data: "{}" })).toBeNull();
  });
});
