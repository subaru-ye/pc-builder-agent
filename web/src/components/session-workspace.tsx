"use client";

import { RunFeedback } from "@/components/run-feedback";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, Bot, Loader2, MessageSquare, PanelRight, RefreshCw, User } from "lucide-react";
import { useSearchParams } from "next/navigation";
import { useCallback, useMemo, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { toast } from "sonner";
import { AppHeader } from "./app-header";
import { BuildInspector } from "./build-inspector";
import { Composer } from "./composer";
import { RequirementForm } from "./requirement-form";
import { Button } from "./ui/button";
import { api, ApiError, exportURL } from "@/lib/api/client";
import { queryKeys } from "@/lib/api/query-keys";
import { userMessage } from "@/lib/api/problem";
import type { PartCategory, RequirementSpec, Run, RunEvent, Session } from "@/lib/api/types";
import { categoryLabels, phaseLabels, stageLabels } from "@/lib/domain";
import { useRunStream } from "@/hooks/use-run-stream";
import { useUIStore } from "@/stores/ui";

export function SessionWorkspace({ sessionID }: { sessionID: string }) {
  const client = useQueryClient();
  const search = useSearchParams();
  const [draft, setDraft] = useState("");
  const [run, setRun] = useState<Run | null>(null);
  const [stage, setStage] = useState<string | null>(null);
  const [pollExpired, setPollExpired] = useState(false);
  const pane = useUIStore((state) => state.mobilePane);
  const setPane = useUIStore((state) => state.setMobilePane);
  const session = useQuery({
    queryKey: queryKeys.session(sessionID), queryFn: () => api.getSession(sessionID), retry: false,
    refetchInterval: (query) => pollExpired && (query.state.data as Session | undefined)?.active_run ? 2000 : false,
  });
  const builds = useQuery({ queryKey: queryKeys.builds(sessionID), queryFn: () => api.listBuilds(sessionID), enabled: !!session.data });
  const latestVersion = useMemo(() => builds.data?.reduce((max, item) => Math.max(max, item.version), 0) || null, [builds.data]);
  const requestedVersion = Number(search.get("version")) || latestVersion;
  const build = useQuery({ queryKey: queryKeys.build(sessionID, requestedVersion ?? 0), queryFn: () => api.getBuild(sessionID, requestedVersion!), enabled: !!requestedVersion });
  const currentRun = run?.status === "running" ? run : session.data?.active_run;

  const refreshSession = useCallback(async () => { await client.invalidateQueries({ queryKey: queryKeys.session(sessionID) }); }, [client, sessionID]);
  const refreshBuilds = useCallback(async (version?: number) => {
    await Promise.all([client.invalidateQueries({ queryKey: queryKeys.session(sessionID) }), client.invalidateQueries({ queryKey: queryKeys.builds(sessionID) })]);
    if (version) await client.invalidateQueries({ queryKey: queryKeys.build(sessionID, version) });
    await client.invalidateQueries({ queryKey: ["diff", sessionID] });
  }, [client, sessionID]);
  const onEvent = useCallback((event: RunEvent) => {
    if (event.event === "run.progress") setStage(String(event.data.payload.stage ?? ""));
    if (event.event === "requirement.ready" || event.event === "assistant.completed") void refreshSession();
    if (event.event === "build.saved") void refreshBuilds(Number(event.data.payload.version));
    if (event.event === "run.failed") toast.error(String(event.data.payload.title ?? "运行失败"));
    if (event.event === "run.completed") { setRun(null); setStage(null); setPollExpired(false); void client.invalidateQueries({ queryKey: queryKeys.run(event.data.run_id) }); void refreshSession(); }
  }, [client, refreshBuilds, refreshSession]);
  const connection = useRunStream(currentRun, onEvent, () => setPollExpired(true));

  const send = useMutation({
    mutationFn: (text: string) => api.sendMessage(sessionID, text, crypto.randomUUID()),
    onSuccess: (nextRun) => { setDraft(""); setRun(nextRun); setStage(nextRun.kind === "screening" ? "screening" : "remote_processing"); void refreshSession(); },
  });
  const saveRequirement = async (value: RequirementSpec) => {
    try {
      await api.replaceRequirement(sessionID, value, crypto.randomUUID());
      await refreshSession();
      toast.success("需求已保存");
    } catch (error) {
      toast.error(userMessage(error));
      throw error;
    }
  };
  const confirm = useMutation({
    mutationFn: async ({ value, dirty }: { value: RequirementSpec; dirty: boolean }) => {
      if (dirty) await api.replaceRequirement(sessionID, value, crypto.randomUUID());
      return api.confirmRequirement(sessionID, crypto.randomUUID());
    },
    onSuccess: (nextRun) => { setRun(nextRun); setStage("remote_processing"); void refreshSession(); },
  });
  const retry = () => {
    const snapshot = session.data;
    const recovery = snapshot?.recovery_phase;
    if (recovery === "requirement_ready" && snapshot?.pending_requirement) confirm.mutate({ value: snapshot.pending_requirement, dirty: false });
    else {
      const previous = [...(snapshot?.messages ?? [])].reverse().find((message) => message.role === "user")?.content;
      if (previous) send.mutate(previous);
    }
  };
  const replace = (category: PartCategory) => {
    const text = `把${categoryLabels[category]}换成……，其他配件尽量不动`;
    setDraft(text);
    setPane("chat");
    window.setTimeout(() => {
      const input = document.getElementById("message-composer") as HTMLTextAreaElement | null;
      input?.focus();
      const start = text.indexOf("……");
      input?.setSelectionRange(start, start + 2);
    });
  };

  if (session.isPending) return <main className="flex min-h-screen items-center justify-center"><Loader2 className="animate-spin text-[var(--ink-muted)]" /><span className="ml-2 text-[var(--ink-muted)]">正在载入会话</span></main>;
  if (session.isError || !session.data) return <main className="flex min-h-screen items-center justify-center p-6 text-center"><div><AlertTriangle className="mx-auto status-fail" /><h1 className="mt-4 text-lg font-semibold">无法打开会话</h1><p className="mt-2 text-[var(--ink-muted)]">{userMessage(session.error)}</p><Button className="mt-5" onClick={() => void session.refetch()}><RefreshCw size={15} />重试</Button></div></main>;

  const data = session.data;
  const busy = data.phase === "building" || data.phase === "changing" || !!currentRun;
  const canSend = data.phase === "collecting" || data.phase === "ready" || data.phase === "requirement_ready" || (data.phase === "error" && data.recovery_phase !== "requirement_ready");
  return <main className="flex h-dvh flex-col overflow-hidden bg-[var(--canvas)]">
    <AppHeader title={data.title} exportHref={requestedVersion ? exportURL(sessionID, requestedVersion) : undefined} share={build.data && requestedVersion ? { sessionID, version: requestedVersion } : undefined} />
    {data.degraded && <div role="status" className="border-b border-[var(--review)]/40 bg-[var(--review)]/10 px-4 py-2 text-center text-xs status-review">Redis 当前不可用：本进程内可以继续使用，但刷新或重启后的会话恢复受限。</div>}
    <div className="flex min-h-11 shrink-0 border-b lg:hidden" aria-label="移动端面板切换"><button className={`flex flex-1 items-center justify-center gap-2 ${pane === "chat" ? "bg-[var(--surface-2)]" : "text-[var(--ink-muted)]"}`} onClick={() => setPane("chat")}><MessageSquare size={16} />对话</button><button className={`flex flex-1 items-center justify-center gap-2 ${pane === "build" ? "bg-[var(--surface-2)]" : "text-[var(--ink-muted)]"}`} onClick={() => setPane("build")}><PanelRight size={16} />配置</button></div>
    <div className="workspace-grid grid min-h-0 flex-1">
      <section className={`${pane === "build" ? "hidden lg:flex" : "flex"} min-h-0 flex-col border-r`} aria-label="会话">
        <div className="min-h-0 flex-1 overflow-y-auto px-4 py-5 sm:px-6">
          <div className="mb-4 flex items-center justify-between text-xs text-[var(--ink-muted)]"><span>{phaseLabels[data.phase]}</span><span>共 {data.version_count} 个版本</span></div>
          <div className="space-y-5">{data.messages.map((message) => <article key={message.id} className={`flex gap-3 ${message.role === "user" ? "pl-8" : "pr-8"}`}><div className="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-md border bg-[var(--surface-1)]">{message.role === "user" ? <User size={14} /> : <Bot size={14} />}</div><div className="min-w-0 flex-1"><div className="mb-1 text-xs text-[var(--ink-subtle)]">{message.role === "user" ? "你" : "装机助手"}</div><div className="prose-chat whitespace-pre-wrap text-sm"><ReactMarkdown remarkPlugins={[remarkGfm]}>{message.content}</ReactMarkdown></div>{message.role === "assistant" && message.run_id && message.run_id !== currentRun?.id && <RunFeedback runID={message.run_id} />}</div></article>)}</div>
          {busy && <RunProgress stage={stage} connection={connection} />}
          {data.phase === "requirement_ready" && <div className="mt-6 rounded-md border border-[var(--primary)]/45 bg-[var(--primary)]/5 p-4 text-sm"><p className="font-medium">需求已经整理好</p><p className="mt-1 text-[var(--ink-muted)]">请在配置面板核对并确认，确认前不会生成配置。</p><Button variant="outline" className="mt-3 lg:hidden" onClick={() => setPane("build")}>打开需求表</Button></div>}
          {data.phase === "error" && <div role="alert" className="mt-6 border-l-2 border-l-[var(--error)] bg-[var(--surface-1)] p-4"><p className="font-medium status-fail">{data.last_error?.title ?? "本次运行失败"}</p><p className="mt-1 text-sm text-[var(--ink-muted)]">{data.last_error ? userMessage(new ApiError(data.last_error)) : "已保存此前数据，你可以显式重试。"}</p><Button variant="outline" className="mt-3" disabled={send.isPending || confirm.isPending} onClick={retry}><RefreshCw size={15} />重试上一步</Button></div>}
          {(send.isError || confirm.isError) && <p role="alert" className="mt-4 status-fail">{userMessage(send.error ?? confirm.error)}</p>}
        </div>
        <div className="shrink-0"><Composer value={draft} onChange={setDraft} onSend={() => send.mutate(draft.trim())} disabled={busy || !canSend || send.isPending} placeholder={data.phase === "ready" ? "描述要改的零件、预算或偏好…" : "继续补充预算、用途和偏好…"} /></div>
      </section>
      <aside className={`${pane === "chat" ? "hidden lg:block" : "block"} min-h-0 overflow-hidden`}>
        {data.phase === "requirement_ready" && data.pending_requirement ? <div className="h-full overflow-y-auto bg-[var(--surface-1)]"><RequirementForm value={data.pending_requirement} busy={confirm.isPending} onSave={saveRequirement} onConfirm={(value, dirty) => confirm.mutateAsync({ value, dirty }).then(() => undefined)} /></div> : <BuildInspector sessionID={sessionID} builds={builds.data ?? []} build={build.data} latestVersion={latestVersion} canChange={data.phase === "ready"} onReplace={replace} />}
      </aside>
    </div>
  </main>;
}

function RunProgress({ stage, connection }: { stage: string | null; connection: string }) {
  const message = connection === "unstable" ? "连接不稳定，正在自动重连" : connection === "long" ? "任务仍在服务端执行，已超过 60 秒" : connection === "polling" ? "事件已过期，正在查询最终状态" : stageLabels[stage ?? ""] ?? "正在处理";
  return <div role="status" aria-live="polite" className="mt-6 flex items-center gap-3 border-l-2 border-l-[var(--primary)] bg-[var(--surface-1)] p-4"><Loader2 className="animate-spin text-[var(--primary)]" size={17} /><div><p className="text-sm font-medium">{message}</p><p className="mt-0.5 text-xs text-[var(--ink-muted)]">离开页面不会取消任务，返回后会自动恢复。</p></div></div>;
}
