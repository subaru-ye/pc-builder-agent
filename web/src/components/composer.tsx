"use client";

import { Send } from "lucide-react";
import { useRef, useState } from "react";
import { Button } from "./ui/button";
import { Textarea } from "./ui/textarea";

export function Composer({ value, onChange, onSend, disabled, placeholder = "描述预算、用途、分辨率和偏好…" }: {
  value: string; onChange: (value: string) => void; onSend: () => void; disabled?: boolean; placeholder?: string;
}) {
  const composing = useRef(false);
  const [focused, setFocused] = useState(false);
  return (
    <div className={`border-t bg-[var(--surface-1)] p-3 transition-colors ${focused ? "border-t-[var(--hairline-strong)]" : ""}`}>
      <label className="sr-only" htmlFor="message-composer">输入需求或改单内容</label>
      <div className="flex items-end gap-2">
        <Textarea id="message-composer" value={value} disabled={disabled} placeholder={placeholder} rows={3} maxLength={4000}
          onChange={(e) => onChange(e.target.value)} onFocus={() => setFocused(true)} onBlur={() => setFocused(false)}
          onCompositionStart={() => { composing.current = true; }} onCompositionEnd={() => { composing.current = false; }}
          onKeyDown={(e) => { if (e.key === "Enter" && !e.shiftKey && !composing.current) { e.preventDefault(); if (!disabled && value.trim()) onSend(); } }}
          className="min-h-20 resize-none bg-[var(--canvas)]" />
        <Button aria-label="发送" size="icon" disabled={disabled || !value.trim()} onClick={onSend} className="h-11 w-11 shrink-0"><Send size={17} /></Button>
      </div>
      <p className="mt-2 text-xs text-[var(--ink-subtle)]">Enter 发送，Shift+Enter 换行</p>
    </div>
  );
}
