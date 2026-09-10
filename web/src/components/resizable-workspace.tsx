"use client";

import { createContext, useContext, useEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";

type Side = "left" | "right";
type Widths = Partial<Record<Side, number>>;
const storageKey = "pc-builder.sidebar-widths.v1";
const clamp = (value: number, min: number, max: number) => Math.max(min, Math.min(value, max));

// Keep at least 360px for conversation reading, even after shrinking the window.
export function sidebarLayout(width: number, preferred: Widths, overview = false) {
  const rightVisible = width >= (overview ? 1280 : 1024);
  const left = clamp(preferred.left ?? 240, 200, Math.min(360, width - 360 - (rightVisible ? 280 : 0)));
  const rightMax = Math.min(640, width - left - 360);
  const right = rightVisible ? clamp(preferred.right ?? (overview ? 320 : clamp(width * .34, 360, 480)), 280, rightMax) : 0;
  return { left, right, leftMax: Math.min(360, width - 360 - right), rightMax };
}

type Layout = ReturnType<typeof sidebarLayout>;
const ResizeContext = createContext<{ layout: Layout; ready: boolean; setWidth: (side: Side, value: number | undefined) => void } | null>(null);

export function ResizableWorkspace({ children, overview = false }: { children: ReactNode; overview?: boolean }) {
  const container = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(1440);
  const [preferred, setPreferred] = useState<Widths>({});
  const [ready, setReady] = useState(false);
  useEffect(() => {
    const frame = requestAnimationFrame(() => {
      try {
        const saved = JSON.parse(localStorage.getItem(storageKey) ?? "{}");
        setPreferred({ left: Number.isFinite(saved?.left) ? saved.left : undefined, right: Number.isFinite(saved?.right) ? saved.right : undefined });
      } catch { /* Storage is optional; resizing still works in restricted browsers. */ }
      setReady(true);
    });
    const observer = new ResizeObserver(([entry]) => setWidth(entry.contentRect.width));
    if (container.current) observer.observe(container.current);
    return () => { cancelAnimationFrame(frame); observer.disconnect(); };
  }, []);
  const layout = sidebarLayout(width, preferred, overview);
  const update = (side: Side, value: number | undefined) => {
    const next = { ...preferred, [side]: value };
    setPreferred(next);
    try { localStorage.setItem(storageKey, JSON.stringify(next)); } catch { /* Best-effort appearance preference only. */ }
  };
  return <ResizeContext.Provider value={{ layout, ready, setWidth: update }}>
    <div ref={container} className={`${overview ? "home-content-grid grid" : "flex"} min-h-0 flex-1`} style={{ "--navigation-width": `${layout.left}px`, "--inspector-width": `${layout.right}px` } as CSSProperties}>{children}</div>
  </ResizeContext.Provider>;
}

export function SidebarResizeHandle({ side, controls }: { side: Side; controls: string }) {
  const context = useContext(ResizeContext);
  const drag = useRef<{ x: number; width: number } | null>(null);
  const stop = () => { drag.current = null; document.body.classList.remove("workspace-resizing"); };
  useEffect(() => () => { if (drag.current) document.body.classList.remove("workspace-resizing"); }, []);
  if (!context) return null;
  const { layout, ready, setWidth } = context;
  const min = side === "left" ? 200 : 280;
  const max = side === "left" ? layout.leftMax : layout.rightMax;
  const direction = side === "left" ? 1 : -1;
  const resize = (value: number) => setWidth(side, clamp(value, min, max));
  return <div role="separator" tabIndex={ready ? 0 : -1} aria-disabled={!ready} style={{ pointerEvents: ready ? undefined : "none" }} aria-orientation="vertical" aria-label={side === "left" ? "调整会话列表宽度" : "调整右侧栏宽度"} aria-controls={controls} aria-valuemin={min} aria-valuemax={Math.max(min, max)} aria-valuenow={Math.round(layout[side])}
    title="拖动调整宽度；方向键微调；双击恢复默认" className={`sidebar-resize-handle ${side === "left" ? "-right-1" : "-left-1"}`}
    onPointerDown={(event) => {
      if (event.button !== 0) return;
      event.preventDefault(); event.currentTarget.focus(); event.currentTarget.setPointerCapture(event.pointerId);
      drag.current = { x: event.clientX, width: layout[side] }; document.body.classList.add("workspace-resizing");
    }}
    onPointerMove={(event) => { if (drag.current) resize(drag.current.width + direction * (event.clientX - drag.current.x)); }}
    onPointerUp={(event) => { stop(); if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId); }}
    onPointerCancel={stop} onLostPointerCapture={stop}
    onDoubleClick={() => setWidth(side, undefined)}
    onKeyDown={(event) => {
      if (event.key === "ArrowLeft" || event.key === "ArrowRight") { event.preventDefault(); resize(layout[side] + direction * (event.key === "ArrowRight" ? 16 : -16)); }
      if (event.key === "Home") { event.preventDefault(); resize(min); }
      if (event.key === "End") { event.preventDefault(); resize(max); }
      if (event.key === "Enter") { event.preventDefault(); setWidth(side, undefined); }
    }} />;
}
