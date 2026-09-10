"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Archive, Check, Clock3, Menu, Plus, X } from "lucide-react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useRef, useState } from "react";
import { useHydrated } from "@/hooks/use-hydrated";
import { api } from "@/lib/api/client";
import { userMessage } from "@/lib/api/problem";
import { queryKeys } from "@/lib/api/query-keys";
import { phaseLabels } from "@/lib/domain";
import { useUIStore } from "@/stores/ui";
import { LocalProfileMenu } from "./local-profile-menu";
import { SessionActions } from "./session-actions";
import { SidebarResizeHandle } from "./resizable-workspace";
import { Button } from "./ui/button";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle, DialogTrigger } from "./ui/dialog";

type NavigationProps = { currentSessionID?: string };

function resetSessionView() {
  useUIStore.setState({ mobilePane: "chat", inspectorTab: "build", diffFrom: null, diffTo: null });
}

export function NewSessionButton({ onCreated, className }: { onCreated?: () => void; className?: string }) {
  // SSR can paint this button before its mutation handler is ready.
  const hydrated = useHydrated();
  const router = useRouter();
  const queryClient = useQueryClient();
  const createKey = useRef<string | null>(null);
  const create = useMutation({
    mutationFn: () => {
      createKey.current ??= crypto.randomUUID();
      return api.createSession(createKey.current);
    },
    onSuccess: (session) => {
      createKey.current = null;
      queryClient.setQueryData(queryKeys.session(session.id), session);
      void queryClient.invalidateQueries({ queryKey: queryKeys.sessions });
      resetSessionView();
      onCreated?.();
      router.push(`/s/${encodeURIComponent(session.id)}`);
    },
  });

  return <div className={className}>
    <Button variant="outline" className="min-h-11 w-full justify-start gap-2" disabled={!hydrated || create.isPending} onClick={() => create.mutate()}>
      <Plus size={16} aria-hidden="true" />{create.isPending ? "正在新建…" : "新建对话"}
    </Button>
    {create.isError && <p role="alert" className="mt-2 text-xs status-fail">{userMessage(create.error)} 可点击新建对话重试。</p>}
  </div>;
}

function NavigationContent({ currentSessionID, onNavigate }: NavigationProps & { onNavigate?: () => void }) {
  const [archived, setArchived] = useState(false);
  const sessions = useQuery({ queryKey: archived ? [...queryKeys.sessions, "archived"] : queryKeys.sessions, queryFn: archived ? api.listArchivedSessions : api.listSessions });
  const navigate = () => { resetSessionView(); onNavigate?.(); };

  return <>
    <div className="shrink-0 p-3"><NewSessionButton onCreated={onNavigate} /></div>
    <nav aria-label={archived ? "已归档会话" : "最近会话"} className="min-h-0 flex-1 overflow-y-auto px-3 pb-4">
      <div className="flex min-h-11 items-center justify-between gap-1 px-2 text-xs font-medium text-[var(--ink-muted)]"><span className="flex items-center gap-2">{archived ? <Archive size={14} aria-hidden="true" /> : <Clock3 size={14} aria-hidden="true" />}{archived ? "已归档" : "最近会话"}</span><Button variant="ghost" size="xs" className="min-h-11 text-xs" onClick={() => setArchived(!archived)}>{archived ? "返回最近" : "已归档"}</Button></div>
      {sessions.isPending && <p role="status" className="px-2 py-3 text-sm text-[var(--ink-muted)]">正在读取会话…</p>}
      {sessions.isError && <div role="alert" className="space-y-2 px-2 py-3 text-sm"><p className="status-fail">会话列表读取失败，已有对话仍保留。</p><Button variant="outline" className="min-h-11" disabled={sessions.isFetching} onClick={() => void sessions.refetch()}>重试读取</Button></div>}
      {sessions.data?.length ? <ul className="space-y-1">{sessions.data.map((session) => {
        const current = currentSessionID === session.id;
        return <li key={session.id} className="relative"><Link
          href={`/s/${encodeURIComponent(session.id)}`}
          aria-current={current ? "page" : undefined}
          onNavigate={navigate}
          className={`block rounded-lg py-3 pr-12 pl-3 outline-none transition-colors focus-visible:ring-2 focus-visible:ring-[var(--primary)] ${current ? "bg-[var(--surface-2)] text-[var(--ink)]" : "hover:bg-[var(--surface-2)]"}`}
        >
          <span className="flex items-center gap-2"><span className="min-w-0 flex-1 truncate text-sm">{session.title || "新对话"}</span>{current && <Check size={14} aria-label="当前会话" className="shrink-0 text-[var(--primary)]" />}</span>
          <span className="mt-1 flex justify-between gap-2 text-xs text-[var(--ink-subtle)]"><span>{phaseLabels[session.phase]}</span><span>{session.version_count} 版</span></span>
        </Link><SessionActions session={session} current={current} onLeave={navigate} /></li>;
      })}</ul> : !sessions.isPending && !sessions.isError && <p className="px-2 py-3 text-sm leading-6 text-[var(--ink-muted)]">{archived ? "还没有已归档的对话。" : "还没有会话。新建对话，或直接发送第一条需求。"}</p>}
    </nav>
    <div className="shrink-0 border-t p-3"><LocalProfileMenu /></div>
  </>;
}

export function SessionNavigation(props: NavigationProps) {
  return <aside id="session-navigation" aria-label="会话导航" className="relative hidden min-h-0 w-[var(--navigation-width,240px)] shrink-0 flex-col border-r bg-[var(--surface-1)] lg:flex"><NavigationContent {...props} /><SidebarResizeHandle side="left" controls="session-navigation" /></aside>;
}

export function SessionNavigationTrigger(props: NavigationProps) {
  const hydrated = useHydrated();
  const [open, setOpen] = useState(false);
  return <Dialog open={open} onOpenChange={setOpen}>
    <DialogTrigger asChild><Button disabled={!hydrated} variant="ghost" size="icon" className="h-11 w-11 lg:hidden" aria-label="打开会话列表" title="会话列表"><Menu size={18} /></Button></DialogTrigger>
    <DialogContent showCloseButton={false} className="top-0 left-0 flex h-dvh w-80 max-w-[calc(100%-2rem)] translate-x-0 translate-y-0 flex-col gap-0 rounded-none border-r bg-[var(--surface-1)] p-0 data-open:animate-none data-closed:animate-none sm:max-w-80">
      <div className="flex h-14 shrink-0 items-center justify-between border-b px-4"><DialogTitle>会话</DialogTitle><DialogClose asChild><Button variant="ghost" size="icon" className="h-11 w-11" aria-label="关闭会话列表" title="关闭会话列表"><X size={18} /></Button></DialogClose></div>
      <DialogDescription className="sr-only">新建装机对话，或继续已保存的会话。</DialogDescription>
      <NavigationContent {...props} onNavigate={() => setOpen(false)} />
    </DialogContent>
  </Dialog>;
}
