"use client";

import { useQuery } from "@tanstack/react-query";
import {
  AlertTriangle,
  CheckCircle2,
  CircleDashed,
  Clock3,
  Database,
  ListChecks,
  MessageSquareText,
  MinusCircle,
  RefreshCw,
} from "lucide-react";
import Link from "next/link";
import type { ComponentType } from "react";
import { api } from "@/lib/api/client";
import { queryKeys } from "@/lib/api/query-keys";
import type { Readiness, SessionSummary } from "@/lib/api/types";
import { phaseLabels } from "@/lib/domain";

type ActionablePhase = Exclude<SessionSummary["phase"], "ready">;

const phasePriority: Record<ActionablePhase, number> = {
  error: 0,
  requirement_ready: 1,
  building: 2,
  changing: 2,
  collecting: 3,
};

const phaseIcons: Record<ActionablePhase, ComponentType<{ size?: number; className?: string }>> = {
  error: AlertTriangle,
  requirement_ready: ListChecks,
  building: RefreshCw,
  changing: RefreshCw,
  collecting: MessageSquareText,
};

function phaseClass(phase: ActionablePhase) {
  if (phase === "error") return "status-fail";
  if (phase === "requirement_ready") return "status-review";
  return "text-[var(--ink-muted)]";
}

interface StatusRowProps {
  label: string;
  state: "ok" | "review" | "fail" | "pending" | "disabled";
  text: string;
}

function StatusRow({ label, state, text }: StatusRowProps) {
  const Icon = state === "ok"
    ? CheckCircle2
    : state === "review" || state === "fail"
      ? AlertTriangle
      : state === "disabled"
        ? MinusCircle
        : CircleDashed;
  const color = state === "ok"
    ? "status-pass"
    : state === "review"
      ? "status-review"
      : state === "fail"
        ? "status-fail"
        : "text-[var(--ink-subtle)]";

  return <div className="flex min-h-9 items-center justify-between gap-3 py-1.5 text-sm">
    <span className="text-[var(--ink-muted)]">{label}</span>
    <span className={`flex items-center gap-1.5 text-xs ${color}`}>
      <Icon size={14} aria-hidden="true" />
      {text}
    </span>
  </div>;
}

function dependencyState(value: "ok" | "degraded" | "unavailable" | undefined) {
  if (value === "ok") return { state: "ok" as const, text: "正常" };
  if (value === "degraded") return { state: "review" as const, text: "恢复受限" };
  if (value === "unavailable") return { state: "fail" as const, text: "不可用" };
  return { state: "pending" as const, text: "无法确认" };
}

function RuntimeStatus({ readiness }: { readiness: ReturnType<typeof useQuery<Readiness>> }) {
  if (readiness.isPending) return <div aria-live="polite">
    <StatusRow label="产品 API" state="pending" text="检查中" />
    <StatusRow label="配置生成" state="pending" text="检查中" />
    <StatusRow label="会话恢复" state="pending" text="检查中" />
    <StatusRow label="账号服务" state="pending" text="检查中" />
  </div>;

  if (readiness.isError) return <div aria-live="polite">
    <StatusRow label="产品 API" state="fail" text="不可用" />
    <StatusRow label="配置生成" state="pending" text="无法确认" />
    <StatusRow label="会话恢复" state="pending" text="无法确认" />
    <StatusRow label="账号服务" state="pending" text="无法确认" />
  </div>;

  const buildsvc = dependencyState(readiness.data.dependencies.buildsvc);
  const redis = dependencyState(readiness.data.dependencies.redis);
  const auth = readiness.data.dependencies.auth
    ? dependencyState(readiness.data.dependencies.auth)
    : { state: "disabled" as const, text: "未启用" };

  return <div aria-live="polite">
    <StatusRow label="产品 API" state="ok" text="可用" />
    <StatusRow label="配置生成" {...buildsvc} />
    <StatusRow label="会话恢复" {...redis} />
    <StatusRow label="账号服务" {...auth} />
  </div>;
}

interface WorkspaceOverviewProps {
  sessions?: SessionSummary[];
  sessionsPending: boolean;
  sessionsError: boolean;
}

export function WorkspaceOverview({ sessions, sessionsPending, sessionsError }: WorkspaceOverviewProps) {
  const readiness = useQuery<Readiness>({
    queryKey: queryKeys.readiness,
    queryFn: api.readiness,
    refetchInterval: 30_000,
    retry: false,
  });
  const actionable = (sessions ?? [])
    .filter((session): session is SessionSummary & { phase: ActionablePhase } => session.phase !== "ready")
    .sort((left, right) => phasePriority[left.phase] - phasePriority[right.phase]
      || Date.parse(right.updated_at) - Date.parse(left.updated_at));
  const visible = actionable.slice(0, 4);

  return <div className="flex h-full min-h-0 flex-col overflow-y-auto">
    <section aria-labelledby="overview-pending-heading" className="border-b p-4">
      <div className="mb-3 flex items-center justify-between gap-3">
        <h2 id="overview-pending-heading" className="flex items-center gap-2 text-xs font-medium text-[var(--ink-muted)]">
          <Clock3 size={14} aria-hidden="true" />待继续
        </h2>
        {!sessionsPending && !sessionsError && <span className="text-xs tabular-nums text-[var(--ink-subtle)]">{actionable.length}</span>}
      </div>
      {sessionsPending && <p className="py-2 text-sm text-[var(--ink-subtle)]">正在读取会话…</p>}
      {sessionsError && <p role="status" className="py-2 text-sm status-fail">暂时无法读取会话，请稍后刷新。</p>}
      {!sessionsPending && !sessionsError && visible.length === 0 && <p className="py-2 text-sm leading-6 text-[var(--ink-subtle)]">暂无待处理会话。可以从中央输入框开始新需求。</p>}
      {visible.length > 0 && <div className="divide-y">
        {visible.map((session) => {
          const Icon = phaseIcons[session.phase];
          return <Link
            key={session.id}
            href={`/s/${encodeURIComponent(session.id)}`}
            className="group -mx-2 flex min-h-14 items-center gap-3 rounded-md px-2 py-2 transition-colors hover:bg-[var(--interactive-hover)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--primary)]"
          >
            <Icon size={16} aria-hidden="true" className={`shrink-0 ${phaseClass(session.phase)}`} />
            <span className="min-w-0 flex-1">
              <span className="block truncate text-sm text-[var(--ink)]">{session.title}</span>
              <span className={`mt-0.5 block text-xs ${phaseClass(session.phase)}`}>{phaseLabels[session.phase]}</span>
            </span>
            <span className="shrink-0 text-xs tabular-nums text-[var(--ink-subtle)]">{session.version_count} 版</span>
          </Link>;
        })}
      </div>}
      {actionable.length > visible.length && <p className="mt-2 text-xs text-[var(--ink-subtle)]">另有 {actionable.length - visible.length} 个会话，可在左侧查看。</p>}
    </section>

    <section aria-labelledby="overview-runtime-heading" className="border-b p-4">
      <h2 id="overview-runtime-heading" className="mb-2 flex items-center gap-2 text-xs font-medium text-[var(--ink-muted)]">
        <CheckCircle2 size={14} aria-hidden="true" />运行状态
      </h2>
      <RuntimeStatus readiness={readiness} />
      <p className="mt-2 text-xs text-[var(--ink-subtle)]">每 30 秒自动检查</p>
    </section>

    <section aria-labelledby="overview-data-heading" className="p-4">
      <div className="mb-3 flex items-center justify-between gap-3">
        <h2 id="overview-data-heading" className="flex items-center gap-2 text-xs font-medium text-[var(--ink-muted)]">
          <Database size={14} aria-hidden="true" />数据状态
        </h2>
        <span className="text-xs text-[var(--ink-subtle)]">待接入</span>
      </div>
      <p className="text-sm leading-6 text-[var(--ink-subtle)]">规格与价格更新时间将在数据健康接口接入后显示。</p>
    </section>
  </div>;
}
