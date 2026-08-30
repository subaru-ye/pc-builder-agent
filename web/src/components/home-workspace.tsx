"use client";

import { useMutation, useQuery } from "@tanstack/react-query";
import { ArrowRight, Clock3, Cpu } from "lucide-react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { AppHeader } from "./app-header";
import { Composer } from "./composer";
import { LocalProfileMenu } from "./local-profile-menu";
import { Button } from "./ui/button";
import { api } from "@/lib/api/client";
import { queryKeys } from "@/lib/api/query-keys";
import { phaseLabels } from "@/lib/domain";
import { userMessage } from "@/lib/api/problem";

const examples = ["8000 元，2K 玩黑神话：悟空", "预算 6000，主要剪 4K 视频，尽量安静", "一万元游戏主机，机箱要小，显卡优先"];

export function HomeWorkspace() {
  const router = useRouter();
  const [draft, setDraft] = useState("");
  const sessions = useQuery({ queryKey: queryKeys.sessions, queryFn: api.listSessions });
  const start = useMutation({
    mutationFn: async (text: string) => {
      const session = await api.createSession(crypto.randomUUID());
      await api.sendMessage(session.id, text, crypto.randomUUID());
      return session.id;
    },
    onSuccess: (id) => router.push(`/s/${encodeURIComponent(id)}`),
  });
  return (
    <main className="flex min-h-screen flex-col bg-[var(--canvas)]">
      <AppHeader showAccount={false} />
      <div className="grid flex-1 md:grid-cols-[280px_minmax(0,1fr)] xl:grid-cols-[280px_minmax(0,1fr)_280px] 2xl:grid-cols-[280px_minmax(0,1fr)_320px]">
        <aside className="hidden min-h-[calc(100vh-56px)] border-r bg-[var(--surface-1)] md:flex md:flex-col">
          <div className="min-h-0 flex-1 overflow-y-auto p-4">
            <div className="mb-4 flex items-center gap-2 text-xs font-medium text-[var(--ink-muted)]"><Clock3 size={14} />最近会话</div>
            <div className="space-y-1">
              {sessions.data?.map((session) => <Link key={session.id} href={`/s/${session.id}`} className="block rounded-md px-3 py-2 hover:bg-[var(--surface-2)]">
                <div className="truncate text-sm">{session.title}</div>
                <div className="mt-1 flex justify-between text-xs text-[var(--ink-subtle)]"><span>{phaseLabels[session.phase]}</span><span>{session.version_count} 版</span></div>
              </Link>)}
              {!sessions.isPending && !sessions.data?.length && <p className="px-3 py-2 text-sm text-[var(--ink-subtle)]">发送第一条需求后，会话会显示在这里。</p>}
            </div>
          </div>
          <div className="shrink-0 border-t p-3"><LocalProfileMenu /></div>
        </aside>
        <section className="flex min-h-[calc(100vh-56px)] flex-col justify-between">
          <div className="mx-auto flex w-full max-w-3xl flex-1 flex-col justify-center px-6 py-12 sm:px-10">
            <div className="mb-8 flex h-11 w-11 items-center justify-center rounded-lg border bg-[var(--surface-1)]"><Cpu size={21} /></div>
            <h1 className="max-w-xl text-3xl font-semibold leading-tight tracking-[-0.6px]">把需求整理成可以核对的配置单</h1>
            <p className="mt-3 max-w-2xl text-[var(--ink-muted)]">先确认预算与用途，再生成带价格快照、12 条规则校验和版本记录的方案。</p>
            <div className="mt-8 flex flex-wrap gap-2">
              {examples.map((example) => <Button key={example} variant="outline" onClick={() => setDraft(example)} className="h-auto min-h-10 whitespace-normal text-left">{example}<ArrowRight size={14} /></Button>)}
            </div>
            {start.isError && <p role="alert" className="mt-6 status-fail">{userMessage(start.error)}</p>}
          </div>
          <div className="mx-auto w-full max-w-3xl px-4 pb-4 sm:px-8"><Composer value={draft} onChange={setDraft} disabled={start.isPending} onSend={() => start.mutate(draft.trim())} /></div>
        </section>
        <aside aria-label="辅助侧栏" className="hidden min-h-[calc(100vh-56px)] border-l bg-[var(--surface-1)] xl:block" />
      </div>
    </main>
  );
}
