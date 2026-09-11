"use client";

import { useEffect, useRef, useState } from "react";
import type { Run, RunEvent } from "@/lib/api/types";
import { readRunStream } from "@/lib/api/sse";

const delays = [1000, 2000, 5000, 10000, 15000];

export function useRunStream(run: Run | null | undefined, onEvent: (event: RunEvent) => void, onExpired: () => void) {
  const [connection, setConnection] = useState<"idle" | "connected" | "unstable" | "long" | "polling">("idle");
  const callbacks = useRef({ onEvent, onExpired });
  useEffect(() => { callbacks.current = { onEvent, onExpired }; }, [onEvent, onExpired]);
  const id = run?.id;
  const status = run?.status;
  const url = run?.events_url;
  useEffect(() => {
    if (!id || status !== "running" || !url) return;
    const controller = new AbortController();
    let stopped = false;
    let lastID: string | undefined;
    const seen = new Set<string>();
    let lastActivity = Date.now();
    let attempt = 0;
    const monitor = window.setInterval(() => {
      const quiet = Date.now() - lastActivity;
      setConnection(quiet >= 60000 ? "long" : quiet >= 30000 ? "unstable" : "connected");
    }, 1000);
    const connect = async () => {
      while (!stopped) {
        try {
          const response = await readRunStream({
            url,
            lastEventID: lastID,
            signal: controller.signal,
            onActivity: () => { lastActivity = Date.now(); setConnection("connected"); },
            onEvent: (event) => {
              if (seen.has(event.id)) return;
              seen.add(event.id);
              lastID = event.id;
              callbacks.current.onEvent(event);
              if (event.event === "run.completed") stopped = true;
            },
          });
          if (response.status === 410) {
            setConnection("polling");
            callbacks.current.onExpired();
            return;
          }
          if (response.ok && stopped) return;
        } catch {
          if (controller.signal.aborted) return;
        }
        const delay = delays[Math.min(attempt, delays.length - 1)];
        attempt += 1;
        await new Promise((resolve) => window.setTimeout(resolve, delay));
      }
    };
    void connect();
    return () => { stopped = true; controller.abort(); window.clearInterval(monitor); };
  // Rendering progress or receiving a fresh copy of the same run must not abort
  // an in-flight stream before its completion event arrives.
  }, [id, status, url]);

  return run?.status === "running" ? connection : "idle";
}
