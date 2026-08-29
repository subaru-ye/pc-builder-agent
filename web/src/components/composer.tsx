"use client";

import { ArrowUp } from "lucide-react";
import { useRef } from "react";
import { Button } from "./ui/button";
import { Textarea } from "./ui/textarea";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

export function Composer({ value, onChange, onSend, disabled, placeholder = "描述预算、用途、分辨率和偏好…" }: {
  value: string; onChange: (value: string) => void; onSend: () => void; disabled?: boolean; placeholder?: string;
}) {
  const composing = useRef(false);
  return (
    <div className="border-t bg-[var(--surface-1)] p-3">
      <label className="sr-only" htmlFor="message-composer">输入需求或改单内容</label>
      <div className="rounded-xl border bg-[var(--canvas)] p-2 transition-colors focus-within:border-[var(--primary)] focus-within:ring-2 focus-within:ring-[var(--primary)]/30">
        <Textarea id="message-composer" value={value} disabled={disabled} placeholder={placeholder} rows={4} maxLength={4000}
          onChange={(e) => onChange(e.target.value)}
          onCompositionStart={() => { composing.current = true; }} onCompositionEnd={() => { composing.current = false; }}
          onKeyDown={(e) => { if (e.key === "Enter" && !e.shiftKey && !composing.current) { e.preventDefault(); if (!disabled && value.trim()) onSend(); } }}
          className="max-h-48 min-h-24 resize-none rounded-none border-0 bg-transparent px-2 py-2 shadow-none focus-visible:border-transparent focus-visible:ring-0 dark:bg-transparent" />
        <div role="group" aria-label="输入操作" className="mt-2 flex min-h-11 items-center justify-between gap-3 px-1">
          <div className="flex min-w-0 items-center gap-2 text-xs text-[var(--ink-subtle)]">
            <span className="hidden sm:inline">Enter 发送 · Shift+Enter 换行</span>
            <span className="sm:hidden">点击右侧按钮发送</span>
            <span aria-label={`已输入 ${value.length} 个字符，共可输入 4000 个字符`} className="tabular whitespace-nowrap">{value.length}/4000</span>
          </div>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button aria-label="发送" size="icon" disabled={disabled || !value.trim()} onClick={onSend} className="h-11 w-11 shrink-0"><ArrowUp size={18} /></Button>
            </TooltipTrigger>
            <TooltipContent side="top">发送消息</TooltipContent>
          </Tooltip>
        </div>
      </div>
    </div>
  );
}
