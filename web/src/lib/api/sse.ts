import type { RunEvent, RunEventName } from "./types";

export interface SSEFrame {
  id?: string;
  event?: string;
  data?: string;
  heartbeat?: boolean;
}

export class SSEParser {
  private buffer = "";
  constructor(private readonly onFrame: (frame: SSEFrame) => void) {}

  feed(chunk: string) {
    this.buffer += chunk.replaceAll("\r\n", "\n");
    let boundary = this.buffer.indexOf("\n\n");
    while (boundary >= 0) {
      const block = this.buffer.slice(0, boundary);
      this.buffer = this.buffer.slice(boundary + 2);
      this.parseBlock(block);
      boundary = this.buffer.indexOf("\n\n");
    }
  }

  private parseBlock(block: string) {
    if (!block) return;
    const frame: SSEFrame = {};
    const data: string[] = [];
    for (const line of block.split("\n")) {
      if (line.startsWith(":")) {
        frame.heartbeat = true;
        continue;
      }
      const colon = line.indexOf(":");
      const field = colon < 0 ? line : line.slice(0, colon);
      const value = colon < 0 ? "" : line.slice(colon + 1).replace(/^ /, "");
      if (field === "id") frame.id = value;
      if (field === "event") frame.event = value;
      if (field === "data") data.push(value);
    }
    if (data.length) frame.data = data.join("\n");
    this.onFrame(frame);
  }
}

const eventNames = new Set<RunEventName>([
  "run.started", "run.progress", "requirement.ready", "assistant.delta",
  "assistant.completed", "build.saved", "run.failed", "run.completed",
]);

export function decodeRunEvent(frame: SSEFrame): RunEvent | null {
  if (!frame.id || !frame.event || !frame.data || !eventNames.has(frame.event as RunEventName)) return null;
  return { id: frame.id, event: frame.event as RunEventName, data: JSON.parse(frame.data) } as RunEvent;
}

export interface StreamOptions {
  url: string;
  lastEventID?: string;
  signal: AbortSignal;
  onEvent: (event: RunEvent) => void;
  onActivity: () => void;
}

export async function readRunStream(options: StreamOptions): Promise<Response> {
  const headers: Record<string, string> = { Accept: "text/event-stream" };
  if (options.lastEventID) headers["Last-Event-ID"] = options.lastEventID;
  const response = await fetch(options.url, { headers, credentials: "include", cache: "no-store", signal: options.signal });
  if (!response.ok || !response.body) return response;
  const seen = new Set<string>();
  const decoder = new TextDecoder();
  const parser = new SSEParser((frame) => {
    options.onActivity();
    const event = decodeRunEvent(frame);
    if (!event || seen.has(event.id)) return;
    seen.add(event.id);
    options.onEvent(event);
  });
  const reader = response.body.getReader();
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    options.onActivity();
    parser.feed(decoder.decode(value, { stream: true }));
  }
  parser.feed(decoder.decode());
  return response;
}
