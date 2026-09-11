import { act, renderHook } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import type { Run, RunEvent } from "@/lib/api/types";
import { readRunStream, type StreamOptions } from "@/lib/api/sse";
import { useRunStream } from "./use-run-stream";

vi.mock("@/lib/api/sse", () => ({ readRunStream: vi.fn() }));
afterEach(() => { vi.useRealTimers(); vi.clearAllMocks(); });

test("progress renders keep the same stream alive and use the latest completion handler", async () => {
  vi.useFakeTimers();
  let stream: StreamOptions | undefined;
  vi.mocked(readRunStream).mockImplementation(options => { stream = options; return new Promise(() => {}); });
  const first = vi.fn();
  const latest = vi.fn();
  const run = { id: "run-1", status: "running", events_url: "/runs/run-1/events" } as Run;
  const { rerender, unmount } = renderHook(({ onEvent }) => useRunStream({ ...run }, onEvent, () => {}), { initialProps: { onEvent: first } });
  await act(async () => { vi.advanceTimersByTime(2000); });
  rerender({ onEvent: latest });
  expect(readRunStream).toHaveBeenCalledTimes(1);
  expect(stream!.signal.aborted).toBe(false);
  const completed: RunEvent = { id: "event-1", event: "run.completed", data: { schema_version: 1, timestamp: "2026-09-11T04:00:00Z", run_id: "run-1", payload: { status: "succeeded" } } };
  act(() => stream!.onEvent(completed));
  expect(first).not.toHaveBeenCalled();
  expect(latest).toHaveBeenCalledWith(completed);
  unmount();
  expect(stream!.signal.aborted).toBe(true);
});
