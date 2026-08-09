"use client";

import { useEffect, useState } from "react";
import type { Run, RunEvent } from "@/lib/api/types";
import { readRunStream } from "@/lib/api/sse";

const delays = [1000, 2000, 5000, 10000, 15000];

export function useRunStream(run: Run | null | undefined, onEvent: (event: RunEvent) => void, onExpired: () => void) {
  const [connection, setConnection] = useState<"idle" | "connected" | "unstable" | "long" | "polling">("idle");
  useEffect(() => {
    if (!run || run.status !== "running") return;
    const controller = new AbortController();
    let stopped = false;
    let lastID: string | undefined;
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
            url: run.events_url,
            lastEventID: lastID,
            signal: controller.signal,
            onActivity: () => { lastActivity = Date.now(); setConnection("connected"); },
            onEvent: (event) => {
              if (event.id === lastID) return;
              lastID = event.id;
              onEvent(event);
              if (event.event === "run.completed") stopped = true;
            },
          });
          if (response.status === 410) {
            setConnection("polling");
            onExpired();
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
  }, [onEvent, onExpired, run]);

  return run?.status === "running" ? connection : "idle";
}
