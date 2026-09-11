"use client";

import { ChatMessage } from "@/components/chat-message";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ArrowLeft, Loader2, MessageSquare, PanelRight, RefreshCw } from "lucide-react";
import { useRouter, useSearchParams } from "next/navigation";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { toast } from "sonner";
import { AppHeader } from "./app-header";
import { BuildInspector, RequirementReadOnly } from "./build-inspector";
import { Composer } from "./composer";
import { ProposalInspector } from "./proposal-inspector";
import { RequirementForm } from "./requirement-form";
import { RequirementStatus, RequirementSummary } from "./requirement-status";
import { SessionNavigation, SessionNavigationTrigger } from "./session-navigation";
import { ResizableWorkspace, SidebarResizeHandle } from "./resizable-workspace";
import { SuggestedPrompts } from "./suggested-prompts";
import { Button } from "./ui/button";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle } from "./ui/dialog";
import { api, ApiError, exportURL } from "@/lib/api/client";
import { queryKeys } from "@/lib/api/query-keys";
import { userMessage } from "@/lib/api/problem";
import type { PartCategory, RequirementOperation, RequirementSpec, Run, RunEvent, Session } from "@/lib/api/types";
import { categoryLabels, phaseLabels, stageLabels } from "@/lib/domain";
import { useRunStream } from "@/hooks/use-run-stream";
import { useUIStore } from "@/stores/ui";

export function SessionWorkspace({ sessionID }: { sessionID: string }) {
  const client = useQueryClient();
  const search = useSearchParams();
  const router = useRouter();
  const [draft, setDraft] = useState("");
  const [run, setRun] = useState<Run | null>(null);
  const [stage, setStage] = useState<string | null>(null);
  const [pollExpired, setPollExpired] = useState(false);
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [detailView, setDetailView] = useState<"requirement" | "build">("requirement");
  const detailTrigger = useRef<HTMLElement | null>(null);
  const afterDetailClose = useRef<(() => void) | null>(null);
  const conversationScroll = useRef<HTMLDivElement>(null);
  const followLatest = useRef(true);
  const setInspectorTab = useUIStore((state) => state.setInspectorTab);
  const session = useQuery({
    queryKey: queryKeys.session(sessionID), queryFn: () => api.getSession(sessionID), retry: false,
    refetchInterval: (query) => pollExpired && (query.state.data as Session | undefined)?.active_run ? 2000 : false,
  });
  const builds = useQuery({ queryKey: queryKeys.builds(sessionID), queryFn: () => api.listBuilds(sessionID), enabled: !!session.data });
  const latestVersion = useMemo(() => builds.data?.reduce((max, item) => Math.max(max, item.version), 0) || null, [builds.data]);
  const requestedVersion = Number(search.get("version")) || latestVersion;
  const build = useQuery({ queryKey: queryKeys.build(sessionID, requestedVersion ?? 0), queryFn: () => api.getBuild(sessionID, requestedVersion!), enabled: !!requestedVersion });
  const currentRun = run?.status === "running" ? run : session.data?.active_run;

  const refreshSession = useCallback(async () => {
    await Promise.all([
      client.invalidateQueries({ queryKey: queryKeys.session(sessionID) }),
      client.invalidateQueries({ queryKey: queryKeys.sessions }),
    ]);
  }, [client, sessionID]);
  const refreshBuilds = useCallback(async (version?: number) => {
    await Promise.all([client.invalidateQueries({ queryKey: queryKeys.session(sessionID) }), client.invalidateQueries({ queryKey: queryKeys.builds(sessionID) })]);
    if (version) await client.invalidateQueries({ queryKey: queryKeys.build(sessionID, version) });
    await client.invalidateQueries({ queryKey: ["diff", sessionID] });
    if (version) {
      setInspectorTab("build");
      const params = new URLSearchParams(search.toString());
      params.delete("version");
      router.replace(`/s/${sessionID}${params.size ? `?${params}` : ""}`, { scroll: false });
    }
  }, [client, sessionID, router, search, setInspectorTab]);
  const onEvent = useCallback((event: RunEvent) => {
    if (event.event === "run.progress") setStage(String(event.data.payload.stage ?? ""));
    if (event.event === "requirement.ready" || event.event === "requirement.updated" || event.event === "assistant.completed") void refreshSession();
    if (event.event === "build.saved") void refreshBuilds(Number(event.data.payload.version));
    if (event.event === "run.failed") toast.error(String(event.data.payload.title ?? "运行失败"));
    if (event.event === "run.completed") { setRun(null); setStage(null); setPollExpired(false); void client.invalidateQueries({ queryKey: queryKeys.run(event.data.run_id) }); void refreshSession(); }
  }, [client, refreshBuilds, refreshSession]);
  const connection = useRunStream(currentRun, onEvent, () => setPollExpired(true));
  useEffect(() => {
    // Browser history and direct links also enter sessions, bypassing sidebar clicks.
    useUIStore.setState({ mobilePane: "chat", inspectorTab: "build", diffFrom: null, diffTo: null });
  }, [sessionID]);
  useEffect(() => {
    const scroll = conversationScroll.current;
    if (scroll && followLatest.current) scroll.scrollTop = scroll.scrollHeight;
  }, [session.data?.messages.length, session.data?.phase, currentRun?.id, build.data?.summary.version]);
  useEffect(() => {
    const desktop = window.matchMedia("(min-width: 1024px)");
    const onResize = () => { if (desktop.matches && detailView === "build") setDetailsOpen(false); };
    desktop.addEventListener("change", onResize);
    return () => desktop.removeEventListener("change", onResize);
  }, [detailView]);

  const send = useMutation({
    mutationFn: (text: string) => api.sendMessage(sessionID, text, crypto.randomUUID()),
    onSuccess: (nextRun) => { followLatest.current = true; setDraft(""); setRun(nextRun); setStage(nextRun.kind === "screening" ? "screening" : "remote_processing"); void refreshSession(); },
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
  const updateRequirement = async (operations: RequirementOperation[]) => {
    const revision = session.data?.requirement_state?.revision;
    if (revision === undefined) return;
    try {
      const next = await api.updateRequirementState(sessionID, revision, operations, crypto.randomUUID());
      client.setQueryData(queryKeys.session(sessionID), next);
      void client.invalidateQueries({ queryKey: queryKeys.sessions });
      toast.success("当前需求已更新");
    } catch (error) {
      await refreshSession();
      throw error;
    }
  };
  const confirm = useMutation({
    mutationFn: async ({ value, dirty }: { value: NonNullable<Session["pending_requirement"]>; dirty: boolean }) => {
      if (dirty && value.schema_version === 1) await api.replaceRequirement(sessionID, value, crypto.randomUUID());
      return api.confirmRequirement(sessionID, crypto.randomUUID());
    },
    onSuccess: (nextRun) => { followLatest.current = true; setRun(nextRun); setStage("remote_processing"); afterDetailClose.current = focusRequirementsEntry; setDetailsOpen(false); setInspectorTab("build"); void refreshSession(); },
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
    const focusComposer = () => {
      const input = document.getElementById("message-composer") as HTMLTextAreaElement | null;
      input?.focus();
      const start = text.indexOf("……");
      input?.setSelectionRange(start, start + 2);
    };
    if (detailsOpen) {
      afterDetailClose.current = focusComposer;
      setDetailsOpen(false);
    } else window.requestAnimationFrame(focusComposer);
  };
  const showSource = (messageID: string) => {
    followLatest.current = false;
    afterDetailClose.current = () => {
      const message = document.getElementById(`message-${messageID}`);
      message?.scrollIntoView({ behavior: "auto", block: "center" });
      message?.focus({ preventScroll: true });
    };
    setDetailsOpen(false);
  };
  const openDetails = (tab: "requirement" | "build") => {
    if (tab === "build") {
      setInspectorTab("build");
      if (window.matchMedia("(min-width: 1024px)").matches) {
        document.getElementById("build-sidebar")?.focus();
        return;
      }
    }
    detailTrigger.current = document.activeElement as HTMLElement | null;
    setDetailView(tab);
    setDetailsOpen(true);
  };
  const shell = (children: ReactNode) => <main className="flex h-dvh flex-col overflow-hidden bg-[var(--canvas)]">
    <AppHeader navigation={<SessionNavigationTrigger currentSessionID={sessionID} />} title={session.data?.title} exportHref={requestedVersion ? exportURL(sessionID, requestedVersion) : undefined} share={build.data && requestedVersion ? { sessionID, version: requestedVersion } : undefined} />
    <ResizableWorkspace>
      <SessionNavigation currentSessionID={sessionID} />
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">{children}</div>
    </ResizableWorkspace>
  </main>;

  if (session.isPending) return shell(<div className="flex flex-1 items-center justify-center"><Loader2 className="animate-spin text-[var(--ink-muted)]" /><span className="ml-2 text-[var(--ink-muted)]">正在载入会话</span></div>);
  if (session.isError || !session.data) return shell(<div className="flex flex-1 items-center justify-center p-6 text-center"><div><AlertTriangle className="mx-auto status-fail" /><h1 className="mt-4 text-lg font-semibold">无法打开会话</h1><p className="mt-2 text-[var(--ink-muted)]">{userMessage(session.error)}</p><Button className="mt-5" onClick={() => void session.refetch()}><RefreshCw size={15} />重试</Button></div></div>);

  const data = session.data;
  const lastMessage = data.messages.at(-1);
  const errorExplainedInChat = !!data.last_error?.detail && lastMessage?.role === "assistant" && !!lastMessage.display_content?.includes(data.last_error.detail);
  const busy = data.phase === "building" || data.phase === "changing" || !!currentRun;
  const canSend = data.phase === "collecting" || data.phase === "ready" || data.phase === "requirement_ready" || data.phase === "error";
  const configuration = <BuildInspector sessionID={sessionID} builds={builds.data ?? []} build={build.data} latestVersion={latestVersion} canChange={data.phase === "ready"} onReplace={replace} />;
  const inspector = data.proposal ? <ProposalInspector key={data.proposal.id} proposal={data.proposal}>{configuration}</ProposalInspector> : configuration;
  const requirements = data.requirement_state
    ? <div className="h-full overflow-y-auto"><RequirementStatus session={data} busy={busy || confirm.isPending || send.isPending} onUpdate={updateRequirement} onSource={showSource} onConfirm={() => data.pending_requirement ? confirm.mutateAsync({ value: data.pending_requirement, dirty: false }).then(() => undefined) : Promise.resolve()} /></div>
    : data.phase === "requirement_ready" && data.pending_requirement?.schema_version === 1
      ? <div className="h-full overflow-y-auto"><RequirementForm value={data.pending_requirement} busy={confirm.isPending} onSave={saveRequirement} onConfirm={(value, dirty) => confirm.mutateAsync({ value, dirty }).then(() => undefined)} /></div>
      : build.data
        ? <div className="h-full overflow-y-auto"><p className="px-4 pt-4 text-sm text-[var(--ink-muted)]">配置 v{build.data.summary.version} 的已确认需求（只读）</p><RequirementReadOnly build={build.data} /></div>
        : <p className="p-6 text-sm text-[var(--ink-muted)]">继续在对话中补充预算和用途，需求整理好后可以在这里核对。</p>;
  return shell(<>
    {data.degraded && <div role="status" className="border-b border-[var(--review)]/40 bg-[var(--review)]/10 px-4 py-2 text-center text-xs status-review">实时事件存储暂不可用：对话与需求仍会保存；重连时将重新读取状态，部分配置修改上下文可能受限。</div>}
    <div className="session-content-grid grid min-h-0 flex-1">
      <section id="conversation-workspace" tabIndex={-1} className="flex min-h-0 min-w-0 flex-col" aria-label="会话">
        <div className="shrink-0 border-b bg-[var(--surface-1)]">
          <div className="mx-auto w-full max-w-4xl">
            {data.requirement_state ? <RequirementSummary session={data} onOpen={() => openDetails("requirement")} /> : <div className="flex items-center justify-between gap-3 px-4 py-2 sm:px-6"><span className="text-sm text-[var(--ink-muted)]">{phaseLabels[data.phase]}</span><Button variant="ghost" size="sm" onClick={() => openDetails("requirement")}>查看需求</Button></div>}
          </div>
        </div>
        <div ref={conversationScroll} className="min-h-0 flex-1 overflow-y-auto" onScroll={(event) => { const scroll = event.currentTarget; followLatest.current = scroll.scrollHeight - scroll.scrollTop - scroll.clientHeight < 96; }}>
          <div className={`mx-auto w-full max-w-4xl px-4 py-5 sm:px-6 ${data.messages.length === 0 ? "flex min-h-full flex-col" : ""}`}>
          <div className={`mb-5 min-h-11 items-center justify-between gap-3 text-xs text-[var(--ink-muted)] ${data.messages.length === 0 ? "flex lg:hidden" : "flex"}`}><span>{data.status_label || phaseLabels[data.phase]}</span><Button variant="ghost" size="sm" className="lg:hidden" aria-label="查看配置详情" onClick={() => openDetails("build")}><PanelRight size={15} />{data.version_count ? `查看配置 · ${data.version_count} 个版本` : "查看配置"}</Button></div>
          {data.messages.length === 0 && <div className="my-auto flex w-full flex-col items-center py-8 text-center" data-testid="conversation-welcome"><MessageSquare size={24} className="mb-4 text-[var(--ink-subtle)]" aria-hidden="true" /><h1 className="text-xl font-semibold">开始新的装机对话</h1><p className="mt-2 max-w-md text-sm text-[var(--ink-muted)]">先说说预算和主要用途，其他偏好可以边聊边补充。</p><SuggestedPrompts centered onSelect={setDraft} /></div>}
          <div className="space-y-5">{data.messages.map((message) => <ChatMessage key={message.id} message={message} activeRunID={currentRun?.id} />)}</div>
          {busy && <RunProgress stage={stage} connection={connection} />}
          {data.phase === "requirement_ready" && (!data.proposal || data.requirement_status === "modified") && <div className="mt-6 rounded-md border border-[var(--primary)]/45 bg-[var(--primary)]/5 p-4 text-sm"><p className="font-medium">可以开始选配</p><p className="mt-1 text-[var(--ink-muted)]">请核对当前需求后开始选配。未知项会保留，可继续讨论。</p><Button variant="outline" className="mt-3" onClick={() => openDetails("requirement")}>核对当前需求</Button></div>}
          {data.phase === "error" && <div role="alert" className="mt-6 border-l-2 border-l-[var(--error)] bg-[var(--surface-1)] p-4"><p className="font-medium status-fail">{data.last_error?.title ?? "本次运行失败"}</p>{!errorExplainedInChat && <p className="mt-1 text-sm text-[var(--ink-muted)]">{data.last_error ? userMessage(new ApiError(data.last_error)) : "已保存此前数据，你可以显式重试。"}</p>}<div className="mt-3 flex flex-wrap gap-2">{data.requirement_state && <Button variant="outline" disabled={busy} onClick={() => openDetails("requirement")}>查看或补充需求</Button>}<Button variant="outline" disabled={send.isPending || confirm.isPending} onClick={retry}><RefreshCw size={15} />重试上一步</Button></div></div>}
          {(send.isError || confirm.isError) && <p role="alert" className="mt-4 status-fail">{userMessage(send.error ?? confirm.error)}</p>}
          </div>
        </div>
        <div className="shrink-0 border-t bg-[var(--surface-1)]"><div className="mx-auto w-full max-w-4xl"><Composer compact value={draft} onChange={setDraft} onSend={() => send.mutate(draft.trim())} disabled={busy || !canSend || send.isPending} placeholder={data.phase === "ready" ? "描述要改的零件、预算或偏好…" : "继续补充预算、用途和偏好…"} /></div></div>
      </section>
      <aside id="build-sidebar" tabIndex={-1} aria-label="配置详情" className="relative hidden min-h-0 min-w-0 flex-col border-l bg-[var(--surface-1)] lg:flex">
        <SidebarResizeHandle side="right" controls="build-sidebar" />
        <div className="flex min-h-14 shrink-0 items-center justify-between gap-3 border-b px-4"><h2 className="font-semibold">配置详情</h2><span className="text-xs text-[var(--ink-muted)]">{data.version_count ? `${data.version_count} 个版本` : "待生成"}</span></div>
        <div className="min-h-0 flex-1">{inspector}</div>
      </aside>
    </div>
      <Dialog open={detailsOpen} onOpenChange={setDetailsOpen}>
        <DialogContent showCloseButton={false} className="inset-y-0 right-0 left-auto flex h-dvh w-full max-w-full translate-x-0 translate-y-0 flex-col gap-0 overflow-hidden rounded-none border-l bg-[var(--surface-1)] p-0 sm:max-w-2xl" onCloseAutoFocus={(event) => { event.preventDefault(); if (afterDetailClose.current) { afterDetailClose.current(); afterDetailClose.current = null; } else if (detailTrigger.current?.isConnected) detailTrigger.current.focus(); else focusRequirementsEntry(); }}>
          <div className="flex min-h-14 shrink-0 items-center justify-between gap-3 border-b px-4 sm:px-6"><div><DialogTitle>{detailView === "requirement" ? "当前需求" : "配置详情"}</DialogTitle><DialogDescription className="sr-only">{detailView === "requirement" ? "查看、修改和确认当前需求。" : "核对配置、校验与历史版本。"}关闭后继续对话。</DialogDescription></div><DialogClose asChild><Button variant="ghost" className="min-h-11" aria-label="关闭详情"><ArrowLeft size={15} />返回对话</Button></DialogClose></div>
          <div className="min-h-0 flex-1">
            {detailView === "requirement" ? requirements : inspector}
          </div>
        </DialogContent>
      </Dialog>
  </>);
}

function focusRequirementsEntry() {
  (document.querySelector<HTMLElement>('[aria-label="需求摘要"] button') ?? document.getElementById("conversation-workspace"))?.focus();
}

function RunProgress({ stage, connection }: { stage: string | null; connection: string }) {
  const message = connection === "unstable" ? "连接不稳定，正在自动重连" : connection === "long" ? "任务仍在服务端执行，已超过 60 秒" : connection === "polling" ? "事件已过期，正在查询最终状态" : stageLabels[stage ?? ""] ?? "正在处理";
  return <div role="status" aria-live="polite" className="mt-6 flex items-center gap-3 border-l-2 border-l-[var(--primary)] bg-[var(--surface-1)] p-4"><Loader2 className="animate-spin text-[var(--primary)]" size={17} /><div><p className="text-sm font-medium">{message}</p><p className="mt-0.5 text-xs text-[var(--ink-muted)]">离开页面不会取消任务，返回后会自动恢复。</p></div></div>;
}
