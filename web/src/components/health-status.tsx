"use client";

import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, CheckCircle2, CircleDashed } from "lucide-react";
import { api } from "@/lib/api/client";
import { queryKeys } from "@/lib/api/query-keys";

export function HealthStatus() {
  const query = useQuery({ queryKey: queryKeys.readiness, queryFn: api.readiness, refetchInterval: 30_000, retry: false });
  if (query.isPending) return <span aria-label="正在检查服务" className="flex shrink-0 items-center gap-2 text-xs text-[var(--ink-muted)]"><CircleDashed size={14} /><span className="hidden sm:inline">检查服务</span></span>;
  if (query.isError || query.data.status === "unavailable") return <span aria-label="服务不可用" className="flex shrink-0 items-center gap-2 text-xs status-fail"><AlertTriangle size={14} /><span className="hidden sm:inline">服务不可用</span></span>;
  if (query.data.status === "degraded") return <span aria-label="会话恢复能力受限" className="flex shrink-0 items-center gap-2 text-xs status-review"><AlertTriangle size={14} /><span className="hidden sm:inline">恢复能力受限</span></span>;
  return <span aria-label="服务就绪" className="flex shrink-0 items-center gap-2 text-xs status-pass"><CheckCircle2 size={14} /><span className="hidden sm:inline">服务就绪</span></span>;
}
