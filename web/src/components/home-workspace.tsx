"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Cpu } from "lucide-react";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { AppHeader } from "./app-header";
import { Composer } from "./composer";
import { NewSessionButton, SessionNavigation, SessionNavigationTrigger } from "./session-navigation";
import { WorkspaceOverview } from "./workspace-overview";
import { ResizableWorkspace, SidebarResizeHandle } from "./resizable-workspace";
import { SuggestedPrompts } from "./suggested-prompts";
import { api } from "@/lib/api/client";
import { queryKeys } from "@/lib/api/query-keys";
import { userMessage } from "@/lib/api/problem";
import { useUIStore } from "@/stores/ui";

export function HomeWorkspace({ showEvaldesk = false }: { showEvaldesk?: boolean }) {
  const router = useRouter();
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState("");
  const sessions = useQuery({ queryKey: queryKeys.sessions, queryFn: api.listSessions });
  const start = useMutation({
    mutationFn: async (text: string) => {
      const session = await api.createSession(crypto.randomUUID());
      await api.sendMessage(session.id, text, crypto.randomUUID());
      return session.id;
    },
    onSuccess: (id) => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.sessions });
      useUIStore.setState({ mobilePane: "chat", inspectorTab: "build", diffFrom: null, diffTo: null });
      router.push(`/s/${encodeURIComponent(id)}`);
    },
  });
  return (
    <main className="flex h-dvh flex-col overflow-hidden bg-[var(--canvas)]">
      <AppHeader showAccount={false} showEvaldesk={showEvaldesk} navigation={<SessionNavigationTrigger />} />
      <ResizableWorkspace overview>
        <SessionNavigation />
        <section className="flex min-h-0 min-w-0 flex-col justify-between">
          <div className="min-h-0 flex-1 overflow-y-auto"><div className="mx-auto flex min-h-full w-full max-w-3xl flex-col justify-center px-6 py-8 sm:px-10 sm:py-12">
            <NewSessionButton className="mb-6 self-start lg:hidden" />
            <div className="mb-8 flex h-11 w-11 items-center justify-center rounded-lg border bg-[var(--surface-1)]"><Cpu size={21} /></div>
            <h1 className="max-w-xl text-3xl font-semibold leading-tight tracking-[-0.6px]">把需求整理成可以核对的配置单</h1>
            <p className="mt-3 max-w-2xl text-[var(--ink-muted)]">先确认预算与用途，再生成带价格快照、12 条规则校验和版本记录的方案。</p>
            <SuggestedPrompts onSelect={setDraft} />
            {start.isError && <p role="alert" className="mt-6 status-fail">{userMessage(start.error)}</p>}
          </div></div>
          <div className="mx-auto w-full max-w-3xl px-4 pb-4 sm:px-8"><Composer value={draft} onChange={setDraft} disabled={start.isPending} onSend={() => start.mutate(draft.trim())} /></div>
        </section>
        <aside id="workspace-overview" aria-label="工作台概览" className="relative hidden min-h-0 border-l bg-[var(--surface-1)] xl:block">
          <SidebarResizeHandle side="right" controls="workspace-overview" />
          <div className="h-full overflow-y-auto"><WorkspaceOverview sessions={sessions.data} sessionsPending={sessions.isPending} sessionsError={sessions.isError} /></div>
        </aside>
      </ResizableWorkspace>
    </main>
  );
}
