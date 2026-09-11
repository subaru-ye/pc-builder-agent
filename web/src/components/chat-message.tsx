"use client";

import { useEffect, useState } from "react";
import { Bot, Check, Copy, User } from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import type { Session } from "@/lib/api/types";
import { MessageAction } from "./message-action";
import { RunFeedback } from "./run-feedback";

export function ChatMessage({ message, activeRunID }: { message: Session["messages"][number]; activeRunID?: string }) {
  const isUser = message.role === "user";
  const content = isUser ? message.content : message.display_content || message.content;
  const copy = <CopyMessage content={content} />;
  return <article id={`message-${message.id}`} tabIndex={-1} aria-label={isUser ? "你的消息" : "装机助手的回复"} className={`flex gap-3 rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--primary)] ${isUser ? "ml-auto w-fit max-w-[90%] flex-row-reverse" : "pr-2 sm:pr-8"}`}>
    <div className="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-md border bg-[var(--surface-1)]" aria-hidden>
      {isUser ? <User size={14} /> : <Bot size={14} />}
    </div>
    <div className="relative min-w-0 flex-1">
      <div className={`mb-1 text-xs text-[var(--ink-subtle)] ${isUser ? "text-right" : ""}`}>{isUser ? "你" : "装机助手"}</div>
      <div className={`prose-chat space-y-3 text-sm [overflow-wrap:anywhere] [&_p]:whitespace-pre-wrap [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:list-decimal [&_ol]:pl-5 [&_li+li]:mt-1 [&_pre]:overflow-x-auto ${isUser ? "rounded-lg bg-[var(--surface-2)] px-3 py-2" : ""}`}>
        <ReactMarkdown remarkPlugins={[remarkGfm]}>{content}</ReactMarkdown>
      </div>
      {!isUser && message.run_id && message.run_id !== activeRunID
        ? <RunFeedback runID={message.run_id} trailingAction={copy} />
        : <div className={`mt-2 flex flex-wrap items-center gap-1 ${isUser ? "justify-end" : ""}`}>{copy}</div>}
    </div>
  </article>;
}

function CopyMessage({ content }: { content: string }) {
  const [status, setStatus] = useState<"idle" | "copied" | "failed">("idle");
  useEffect(() => {
    if (status !== "copied") return;
    const timer = window.setTimeout(() => setStatus("idle"), 2000);
    return () => window.clearTimeout(timer);
  }, [status]);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(content);
      setStatus("copied");
    } catch {
      setStatus("failed");
    }
  };
  return <>
    <MessageAction label="复制这条消息" onClick={() => void copy()}>
      {status === "copied" ? <Check size={14} aria-hidden /> : <Copy size={14} aria-hidden />}
    </MessageAction>
    <span className="sr-only" role="status">{status === "copied" ? "已复制" : ""}</span>
    {status === "failed" && <span role="alert" className="text-xs text-[var(--error)]">复制失败，请重试或手动选择文字。</span>}
  </>;
}
