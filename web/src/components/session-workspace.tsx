"use client";

import { ChatMessage } from "@/components/chat-message";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ArrowLeft, Loader2, MessageSquare, PanelRight, RefreshCw } from "lucide-react";
import { useRouter, useSearchParams } from "next/navigation";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { toast } from "sonner";
import { AppHeader } from "./app-header";
import { BuildInspector } from "./build-inspector";
import { Composer } from "./composer";
import { ProposalInspector } from "./proposal-inspector";
import { PreferencesPanel } from "./preferences-panel";
import { RequirementFullEditor } from "./requirement-full-editor";
import { RequirementReviewPanel } from "./requirement-review-panel";
import { RequirementStatusPane, RequirementSummary } from "./requirement-status";
import { SessionNavigation, SessionNavigationTrigger } from "./session-navigation";
import { ResizableWorkspace, SidebarResizeHandle } from "./resizable-workspace";
import { SuggestedPrompts } from "./suggested-prompts";
import { Button } from "./ui/button";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle } from "./ui/dialog";
import { api, ApiError, exportURL } from "@/lib/api/client";
import { queryKeys } from "@/lib/api/query-keys";
import { userMessage } from "@/lib/api/problem";
import type { PartCategory, RequirementOperation, Run, RunEvent, Session } from "@/lib/api/types";
import { categoryLabels, phaseLabels, stageLabels } from "@/lib/domain";
import { useRunStream } from "@/hooks/use-run-stream";
import { resetWorkspaceUI, useUIStore } from "@/stores/ui";

export function SessionWorkspace({ sessionID }: { sessionID: string }) {
  const client = useQueryClient();
  const search = useSearchParams();
  const router = useRouter();
  const [draft, setDraft] = useState("");
  const [run, setRun] = useState<Run | null>(null);
  const [stage, setStage] = useState<string | null>(null);
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [editorOpen, setEditorOpen] = useState(false);
  const [reviewOpen, setReviewOpen] = useState(false);
  const [reviewHint, setReviewHint] = useState(false);
  const [reviewError, setReviewError] = useState<string | null>(null);
  const [announcement, setAnnouncement] = useState("");
  const [desktop, setDesktop] = useState(true);
  const detailTrigger = useRef<HTMLElement | null>(null);
  const afterDetailClose = useRef<(() => void) | null>(null);
  const confirmKeyRef = useRef<string | null>(null);
  const conversationScroll = useRef<HTMLDivElement>(null);
  const followLatest = useRef(true);
  const workspaceTab = useUIStore((state) => state.workspaceTab);
  const setWorkspaceTab = useUIStore((state) => state.setWorkspaceTab);
  const session = useQuery({
    queryKey: queryKeys.session(sessionID), queryFn: () => api.getSession(sessionID), retry: false,
    // run 活动期间以 2 秒轮询兜底:SSE 仍是主通道,任何一帧丢失都会被下一次轮询收敛。
    refetchInterval: (query) => {
      const data = query.state.data as Session | undefined;
      if (data?.active_run || (run && run.status === "running")) return 2000;
      return false;
    },
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
  // build.saved 只失效缓存并给出完成提示;不切换用户当前的 Tab。
  const refreshBuilds = useCallback(async (version?: number) => {
    await Promise.all([client.invalidateQueries({ queryKey: queryKeys.session(sessionID) }), client.invalidateQueries({ queryKey: queryKeys.builds(sessionID) })]);
    if (version) await client.invalidateQueries({ queryKey: queryKeys.build(sessionID, version) });
    await client.invalidateQueries({ queryKey: ["diff", sessionID] });
    if (version) {
      setAnnouncement("配置已生成，可在配置详情中查看。");
      const params = new URLSearchParams(search.toString());
      params.delete("version");
      router.replace(`/s/${sessionID}${params.size ? `?${params}` : ""}`, { scroll: false });
    }
  }, [client, sessionID, router, search]);

  const focusFirstMissing = useCallback((fields: string[]) => {
    setWorkspaceTab("requirement");
    const key = fields[0] ?? session.data?.requirement_readiness?.missing_fields[0];
    if (!key) return;
    window.requestAnimationFrame(() => {
      const escaped = (window.CSS?.escape ?? ((value: string) => value))(key);
      const row = document.querySelector(`[data-field-row="${escaped}"]`);
      row?.scrollIntoView({ behavior: "auto", block: "center" });
      row?.querySelector<HTMLElement>("button")?.focus({ preventScroll: true });
    });
  }, [session.data?.requirement_readiness?.missing_fields, setWorkspaceTab]);

  const openReview = useCallback(() => {
    // 同一次核定点击/网络重试复用同一幂等键;409 重新核定后换新键。
    confirmKeyRef.current ??= crypto.randomUUID();
    setReviewError(null);
    setReviewHint(false);
    setEditorOpen(false);
    setReviewOpen(true);
  }, []);

  const onEvent = useCallback((event: RunEvent) => {
    if (event.event === "run.progress") setStage(String(event.data.payload.stage ?? ""));
    if (event.event === "requirement.ready" || event.event === "requirement.updated" || event.event === "assistant.completed" || event.event === "requirement.confirmed") void refreshSession();
    if (event.event === "build.saved") void refreshBuilds(Number(event.data.payload.version));
    if (event.event === "run.failed") toast.error(String(event.data.payload.title ?? "运行失败"));
    if (event.event === "run.cancelled") {
      toast.info(String(event.data.payload.title ?? "已停止本次生成"));
      setRun(null); setStage(null); void refreshSession();
    }
    if (event.event === "run.completed") { setRun(null); setStage(null); void client.invalidateQueries({ queryKey: queryKeys.run(event.data.run_id) }); void refreshSession(); }
    // presentation.action 只驱动当前 run 的短期 UI 动作;不据此刷新业务缓存。
    // 动作由服务端按本轮落库后的 readiness 决定,不读会话缓存复核:
    // requirement.updated 触发的刷新未完成时缓存仍可能是旧值(如本轮
    // 刚补齐最后条件),凭缓存判断会把 open_requirement_review 降级丢失。
    if (event.event === "presentation.action") {
      const action = String(event.data.payload.action ?? "");
      const fields = Array.isArray(event.data.payload.fields) ? event.data.payload.fields.map(String) : [];
      if (action === "open_requirement_review") {
        const active = document.activeElement;
        const typing = active instanceof HTMLElement && (active.id === "message-composer" || ["INPUT", "TEXTAREA", "SELECT"].includes(active.tagName));
        if (typing) setReviewHint(true); else openReview();
      }
      if (action === "focus_missing_requirement") focusFirstMissing(fields);
    }
  }, [client, focusFirstMissing, openReview, refreshBuilds, refreshSession]);
  const connection = useRunStream(currentRun, onEvent, () => { setRun(null); void refreshSession(); });
  useEffect(() => {
    // 会话切换或重新进入时重置到需求 Tab(本地状态随 key 重挂载重置)。
    resetWorkspaceUI();
  }, [sessionID]);
  useEffect(() => {
    const scroll = conversationScroll.current;
    if (scroll && followLatest.current) scroll.scrollTop = scroll.scrollHeight;
  }, [session.data?.messages.length, session.data?.phase, currentRun?.id, build.data?.summary.version]);
  useEffect(() => {
    const mq = window.matchMedia("(min-width: 1024px)");
    const update = () => setDesktop(mq.matches);
    update();
    mq.addEventListener("change", update);
    return () => mq.removeEventListener("change", update);
  }, []);
  useEffect(() => {
    const desktopMq = window.matchMedia("(min-width: 1024px)");
    const onResize = () => { if (desktopMq.matches) setDetailsOpen(false); };
    desktopMq.addEventListener("change", onResize);
    return () => desktopMq.removeEventListener("change", onResize);
  }, []);

  const send = useMutation({
    mutationFn: (text: string) => api.sendMessage(sessionID, text, crypto.randomUUID()),
    onSuccess: (nextRun) => {
      followLatest.current = true;
      setDraft("");
      if (nextRun.status === "running") {
        setRun(nextRun);
        setStage(nextRun.kind === "screening" ? "screening" : "remote_processing");
      }
      void refreshSession();
    },
  });
  // expectedRevision 由调用方决定:完整编辑器传打开时的基线 revision(并发
  // 变化必须以 409 暴露);行内编辑等实时场景省略,回退到当前缓存 revision。
  const updateRequirement = useCallback(async (operations: RequirementOperation[], expectedRevision?: number) => {
    const revision = expectedRevision ?? session.data?.requirement_state?.revision;
    if (revision === undefined) return;
    try {
      const next = await api.updateRequirementState(sessionID, revision, operations, crypto.randomUUID());
      // 编辑没有 run/SSE,以返回的完整 Session 替换缓存真值。
      client.setQueryData(queryKeys.session(sessionID), next);
      void client.invalidateQueries({ queryKey: queryKeys.sessions });
      toast.success("当前需求已更新");
    } catch (error) {
      await refreshSession();
      throw error;
    }
  }, [client, refreshSession, session.data?.requirement_state?.revision, sessionID]);

  const confirm = useMutation({
    mutationFn: async ({ retryOfRunID }: { retryOfRunID?: string | null }) => {
      const data = session.data;
      const revision = data?.requirement_state?.revision;
      const hash = data?.review_hash;
      if (revision === undefined || !hash) throw new Error("核定预览尚未就绪");
      const key = confirmKeyRef.current ?? crypto.randomUUID();
      confirmKeyRef.current = key;
      return api.confirmRequirement(sessionID, key, { expected_revision: revision, expected_review_hash: hash, retry_of_run_id: retryOfRunID ?? null });
    },
    onSuccess: (nextRun) => {
      followLatest.current = true;
      if (nextRun.status === "running") {
        setRun(nextRun); setStage("remote_processing");
      } else {
        // 响应返回时 run 已是终态(离线/极速执行):不等一条不会来的 SSE,
        // 直接按合同失效 Session 与 builds 取回真值。
        void refreshBuilds();
      }
      setReviewOpen(false); confirmKeyRef.current = null;
      // 重新确认后历史版本查询参数不再代表当前目标,回到最新视图。
      if (search.get("version")) {
        const params = new URLSearchParams(search.toString());
        params.delete("version");
        router.replace(`/s/${sessionID}${params.size ? `?${params}` : ""}`, { scroll: false });
      }
      setAnnouncement("需求已确认，开始生成配置。");
      void refreshSession();
    },
    onError: (error) => {
      if (error instanceof ApiError && error.problem.status === 409) {
        // 面板不关闭:重新载入最新预览,未提交编辑保留在表单中。
        confirmKeyRef.current = null;
        setReviewError("需求或系统默认已更新，已载入最新预览，请重新核定后再确认。");
        void refreshSession();
      } else {
        setReviewError(userMessage(error));
      }
    },
  });
  const cancel = useMutation({
    mutationFn: (runID: string) => api.cancelRun(sessionID, runID),
    onSuccess: () => { toast.info("已请求停止本次生成"); },
    onError: (error) => { toast.error(userMessage(error)); void refreshSession(); },
  });
  const retry = () => {
    const recovery = session.data?.recovery_phase;
    if (recovery === "requirement_ready" && session.data?.requirement_readiness?.confirmation_eligible) { openReview(); return; }
    const previous = [...(session.data?.messages ?? [])].reverse().find((message) => message.role === "user")?.content;
    if (previous) send.mutate(previous);
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
    const focusMessage = () => {
      const message = document.getElementById(`message-${messageID}`);
      message?.scrollIntoView({ behavior: "auto", block: "center" });
      message?.focus({ preventScroll: true });
    };
    if (desktop) { window.requestAnimationFrame(focusMessage); return; }
    afterDetailClose.current = focusMessage;
    setDetailsOpen(false);
  };
  const shell = (children: ReactNode) => <main className="relative flex h-dvh flex-col overflow-hidden bg-[var(--canvas)]">
    <AppHeader navigation={<SessionNavigationTrigger currentSessionID={sessionID} />} title={session.data?.title} exportHref={requestedVersion ? exportURL(sessionID, requestedVersion) : undefined} share={build.data && requestedVersion ? { sessionID, version: requestedVersion } : undefined} />
    <div aria-live="polite" role="status" className="sr-only">{announcement}</div>
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
  const buildReady = (data.version_count ?? 0) > 0;
  const configuration = <BuildInspector sessionID={sessionID} builds={builds.data ?? []} build={build.data} latestVersion={latestVersion} canChange={data.phase === "ready"} onReplace={replace} relation={data.build_relation} onBackToRequirement={() => setWorkspaceTab("requirement")} />;
  const inspector = data.proposal ? <ProposalInspector key={data.proposal.id} proposal={data.proposal}>{configuration}</ProposalInspector> : configuration;
  const defaults = new Map((data.requirement_readiness?.effective_defaults ?? []).map((item) => [item.field, item.value]));
  // 需求编辑在 Builder 运行期间保持可用(服务端 EditRequirement 明确支持):
  // busy 只锁定网络提交瞬间,不包含 run 活动状态;再次确认启动由
  // PrimaryAction 的 running 分支与后端 admission(ErrSessionBusy)双重禁止。
  const requirementBusy = confirm.isPending || send.isPending;
  const requirementPane = <>
    {data.requirement_state
      ? <RequirementStatusPane session={data} busy={requirementBusy} onUpdate={updateRequirement} onSource={showSource} onOpenReview={openReview} onOpenEditor={() => { setReviewOpen(false); setEditorOpen(true); }} />
      : <p className="p-6 text-sm text-[var(--ink-muted)]">继续在对话中补充预算和用途，需求整理好后可以在这里核对。</p>}
    <PreferencesPanel session={data} />
  </>;
  const sharedInspector = <SharedInspector
    tab={workspaceTab} onTab={(next) => { setWorkspaceTab(next); if (next === "build") setReviewHint(false); }}
    buildEnabled={buildReady}
    requirement={requirementPane}
    build={inspector}
  />;

  const openDetails = (tab: "requirement" | "build") => {
    if (tab === "build" && !buildReady) return;
    if (window.matchMedia("(min-width: 1024px)").matches) {
      setWorkspaceTab(tab);
      document.getElementById("build-sidebar")?.focus();
      return;
    }
    detailTrigger.current = document.activeElement as HTMLElement | null;
    setWorkspaceTab(tab);
    setDetailsOpen(true);
  };
  return shell(<>
    {data.degraded && <div role="status" className="border-b border-[var(--review)]/40 bg-[var(--review)]/10 px-4 py-2 text-center text-xs status-review">实时事件存储暂不可用：对话与需求仍会保存；重连时将重新读取状态，部分配置修改上下文可能受限。</div>}
    <div className="session-content-grid grid min-h-0 flex-1">
      <section id="conversation-workspace" tabIndex={-1} className="flex min-h-0 min-w-0 flex-col" aria-label="会话">
        <div className="shrink-0 border-b bg-[var(--surface-1)]">
          <div className="mx-auto w-full max-w-4xl">
            {data.requirement_state ? <RequirementSummary session={data} onOpen={() => openDetails("requirement")} /> : <div className="flex items-center justify-between gap-3 px-4 py-2 sm:px-6"><span className="text-sm text-[var(--ink-muted)]">{phaseLabels[data.phase]}</span></div>}
          </div>
        </div>
        <div ref={conversationScroll} className="relative min-h-0 flex-1 overflow-y-auto overscroll-contain" onScroll={(event) => { const scroll = event.currentTarget; followLatest.current = scroll.scrollHeight - scroll.scrollTop - scroll.clientHeight < 96; }}>
          <div className={`mx-auto w-full max-w-4xl px-4 py-5 sm:px-6 ${data.messages.length === 0 ? "flex min-h-full flex-col" : ""}`}>
          <div className={`mb-5 min-h-11 items-center justify-between gap-3 text-xs text-[var(--ink-muted)] ${data.messages.length === 0 ? "flex lg:hidden" : "flex"}`}><span>{data.status_label || phaseLabels[data.phase]}</span><Button variant="ghost" size="sm" className="lg:hidden" aria-label="查看配置详情" disabled={!buildReady} onClick={() => openDetails("build")}><PanelRight size={15} />{buildReady ? `查看配置 · ${data.version_count} 个版本` : "生成配置后可查看"}</Button></div>
          {data.messages.length === 0 && <div className="my-auto flex w-full flex-col items-center py-8 text-center" data-testid="conversation-welcome"><MessageSquare size={24} className="mb-4 text-[var(--ink-subtle)]" aria-hidden="true" /><h1 className="text-xl font-semibold">开始新的装机对话</h1><p className="mt-2 max-w-md text-sm text-[var(--ink-muted)]">先说说预算和主要用途，其他偏好可以边聊边补充。</p><SuggestedPrompts centered onSelect={setDraft} /></div>}
          <div className="space-y-5">{data.messages.map((message) => <ChatMessage key={message.id} message={message} activeRunID={currentRun?.id} />)}</div>
          {busy && <RunProgress stage={stage} connection={connection} onCancel={currentRun ? () => cancel.mutate(currentRun.id) : undefined} cancelPending={cancel.isPending} />}
          {reviewHint && <div role="status" className="mt-6 flex flex-wrap items-center justify-between gap-2 border-l-2 border-l-[var(--primary)] bg-[var(--surface-1)] p-4 text-sm"><span>需求已就绪，可以核定并开始配置。</span><Button variant="outline" size="sm" onClick={openReview}>打开核定面板</Button></div>}
          {data.phase === "error" && <div role="alert" className="mt-6 border-l-2 border-l-[var(--error)] bg-[var(--surface-1)] p-4"><p className="font-medium status-fail">{data.last_error?.title ?? "本次运行失败"}</p>{!errorExplainedInChat && <p className="mt-1 text-sm text-[var(--ink-muted)]">{data.last_error ? userMessage(new ApiError(data.last_error)) : "已保存此前数据，你可以显式重试。"}</p>}<div className="mt-3 flex flex-wrap gap-2">{data.requirement_state && <Button variant="outline" disabled={busy} onClick={() => openDetails("requirement")}>查看或补充需求</Button>}<Button variant="outline" disabled={send.isPending || confirm.isPending} onClick={retry}><RefreshCw size={15} />重试上一步</Button></div></div>}
          {(send.isError || confirm.isError) && !reviewOpen && <p role="alert" className="mt-4 status-fail">{userMessage(send.error ?? confirm.error)}</p>}
          </div>
        </div>
        <div className="shrink-0 border-t bg-[var(--surface-1)]"><div className="mx-auto w-full max-w-4xl"><Composer compact value={draft} onChange={setDraft} onSend={() => send.mutate(draft.trim())} disabled={busy || !canSend || send.isPending} placeholder={data.phase === "ready" ? "描述要改的零件、预算或偏好…" : "继续补充预算、用途和偏好…"} /></div></div>
      </section>
      {desktop && <aside id="build-sidebar" tabIndex={-1} aria-label="工作台详情" className="relative hidden min-h-0 min-w-0 flex-col border-l bg-[var(--surface-1)] lg:flex">
        <SidebarResizeHandle side="right" controls="build-sidebar" />
        {sharedInspector}
      </aside>}
    </div>
      {detailsOpen && !desktop && <Dialog open={detailsOpen} onOpenChange={setDetailsOpen}>
        <DialogContent showCloseButton={false} className="inset-y-0 right-0 left-auto flex h-dvh w-full max-w-full translate-x-0 translate-y-0 flex-col gap-0 overflow-hidden rounded-none border-l bg-[var(--surface-1)] p-0 sm:max-w-2xl" onCloseAutoFocus={(event) => { event.preventDefault(); if (afterDetailClose.current) { afterDetailClose.current(); afterDetailClose.current = null; } else if (detailTrigger.current?.isConnected) detailTrigger.current.focus(); }}>
          <div className="flex min-h-14 shrink-0 items-center justify-between gap-3 border-b px-4 sm:px-6"><div><DialogTitle>{workspaceTab === "requirement" ? "需求状态" : "配置详情"}</DialogTitle><DialogDescription className="sr-only">{workspaceTab === "requirement" ? "查看、修改和核定当前需求。" : "核对配置、校验与历史版本。"}关闭后继续对话。</DialogDescription></div><DialogClose asChild><Button variant="ghost" className="min-h-11" aria-label="关闭详情"><ArrowLeft size={15} />返回对话</Button></DialogClose></div>
          <div className="min-h-0 flex-1">{sharedInspector}</div>
        </DialogContent>
      </Dialog>}
      <Dialog open={reviewOpen} onOpenChange={(open) => { if (!open) setReviewOpen(false); }}>
        <DialogContent showCloseButton={false} className="flex max-h-[92dvh] flex-col gap-0 overflow-hidden p-0 sm:max-w-[672px]" onCloseAutoFocus={(event) => {
          event.preventDefault();
          const target = window.matchMedia("(min-width: 1024px)").matches
            ? document.getElementById("workspace-tab-requirement")
            : document.querySelector('[aria-label="需求状态"]');
          if (target instanceof HTMLElement && target.isConnected) target.focus();
        }}>
          <div className="flex min-h-14 shrink-0 items-center justify-between gap-3 border-b px-4 sm:px-6">
            <DialogTitle>核定当前需求</DialogTitle>
            <DialogClose asChild><Button variant="ghost" size="sm" aria-label="返回修改" onClick={() => setReviewOpen(false)}>返回修改</Button></DialogClose>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto">
            {data.review_spec
              ? <RequirementReviewPanel session={data} busy={confirm.isPending} error={reviewError} onBack={() => setReviewOpen(false)} onConfirm={() => {
                const retryEligible = data.requirement_confirmation.status === "confirmed" && data.build_relation.status === "failed";
                confirm.mutate({ retryOfRunID: retryEligible ? data.build_relation.retry_run_id ?? null : null });
              }} />
              : <p className="p-6 text-sm text-[var(--ink-muted)]">核定预览尚未生成，请先补充必要需求。</p>}
          </div>
        </DialogContent>
      </Dialog>
      <Dialog open={editorOpen} onOpenChange={(open) => { if (!open) setEditorOpen(false); }}>
        <DialogContent showCloseButton={false} className="flex max-h-[92dvh] flex-col gap-0 overflow-hidden p-0 sm:max-w-[672px]">
          <div className="flex min-h-14 shrink-0 items-center justify-between gap-3 border-b px-4 sm:px-6">
            <DialogTitle>编辑全部需求</DialogTitle>
            <DialogClose asChild><Button variant="ghost" size="sm" aria-label="关闭编辑" onClick={() => setEditorOpen(false)}>关闭</Button></DialogClose>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto">
            <RequirementFullEditor session={data} defaults={defaults} busy={requirementBusy} onSave={async (operations, expectedRevision) => { await updateRequirement(operations, expectedRevision); setEditorOpen(false); }} onClose={() => setEditorOpen(false)} />
          </div>
        </DialogContent>
      </Dialog>
  </>);
}

/** 右栏共享 inspector:顶层"需求状态 / 配置详情"双 Tab,≥1024 常驻、窄屏入抽屉。 */
function SharedInspector({ tab, onTab, buildEnabled, requirement, build }: {
  tab: "requirement" | "build";
  onTab: (tab: "requirement" | "build") => void;
  buildEnabled: boolean;
  requirement: ReactNode;
  build: ReactNode;
}) {
  const tabs = [["requirement", "需求状态"], ["build", "配置详情"]] as const;
  return <>
    <div className="flex min-h-11 shrink-0 items-center justify-between gap-3 overflow-x-auto border-b px-2" role="tablist" aria-label="工作台详情">
      {tabs.map(([key, label]) => {
        const disabled = key === "build" && !buildEnabled;
        return <button key={key} id={`workspace-tab-${key}`} role="tab" aria-selected={tab === key} disabled={disabled} aria-disabled={disabled}
          aria-describedby={disabled ? "build-tab-disabled-reason" : undefined}
          title={disabled ? "生成配置后可查看" : undefined}
          className={`min-h-11 shrink-0 border-b-2 px-4 text-sm ${disabled ? "cursor-not-allowed text-[var(--ink-subtle)]" : tab === key ? "border-b-[var(--primary)] text-[var(--ink)]" : "border-b-transparent text-[var(--ink-muted)]"}`}
          onClick={() => !disabled && onTab(key)}>{label}</button>;
      })}
      {!buildEnabled && <span id="build-tab-disabled-reason" className="ml-auto shrink-0 pr-2 text-xs text-[var(--ink-subtle)]">生成配置后可查看</span>}
    </div>
    <div className="min-h-0 flex-1 overflow-y-auto" role="tabpanel" aria-label={tab === "requirement" ? "需求状态" : "配置详情"}>{tab === "requirement" ? requirement : build}</div>
  </>;
}

function RunProgress({ stage, connection, onCancel, cancelPending }: { stage: string | null; connection: string; onCancel?: () => void; cancelPending: boolean }) {
  const message = connection === "unstable" ? "连接不稳定，正在自动重连" : connection === "long" ? "任务仍在服务端执行，已超过 60 秒" : connection === "polling" ? "事件已过期，正在查询最终状态" : stageLabels[stage ?? ""] ?? "正在处理";
  return <div role="status" aria-live="polite" className="mt-6 flex items-center gap-3 border-l-2 border-l-[var(--primary)] bg-[var(--surface-1)] p-4"><Loader2 className="animate-spin text-[var(--primary)]" size={17} /><div className="min-w-0 flex-1"><p className="text-sm font-medium">{message}</p><p className="mt-0.5 text-xs text-[var(--ink-muted)]">离开页面不会取消任务，返回后会自动恢复。</p></div>{onCancel && <Button variant="outline" size="sm" disabled={cancelPending} onClick={onCancel}>{cancelPending ? "正在停止…" : "停止本次生成"}</Button>}</div>;
}
