import { AlertTriangle, Clock3 } from "lucide-react";
import type { components } from "@/lib/api/generated";

type PriceFreshnessSummary = components["schemas"]["PriceFreshnessSummary"];
type PriceFreshness = components["schemas"]["PriceFreshness"];

const labels: Record<PriceFreshness, string> = {
  fresh: "价格观察在 7 天内",
  aging: "价格可能已变化，请购买前重新核价",
  stale: "价格快照已过期，请购买前重新核价",
  unknown: "部分价格无法关联观察日期，请购买前重新核价",
};

export function freshnessLabel(value?: PriceFreshness) {
  return value ? labels[value] : labels.unknown;
}

export function PriceFreshnessNotice({ value, compact = false }: { value?: PriceFreshnessSummary; compact?: boolean }) {
  const status = value?.overall ?? "unknown";
  const warning = status !== "fresh";
  const Icon = warning ? AlertTriangle : Clock3;
  return <div className={`${compact ? "px-3 py-2 text-xs" : "px-4 py-3 text-sm"} flex gap-2 border ${status === "stale" ? "border-[var(--fail)]/45 bg-[var(--fail)]/10 status-fail" : warning ? "border-[var(--review)]/45 bg-[var(--review)]/10 status-review" : "border-[var(--hairline-strong)] text-[var(--ink-muted)]"}`} role={warning ? "status" : undefined}>
    <Icon aria-hidden className="mt-0.5 shrink-0" size={compact ? 14 : 16} />
    <div><p>{freshnessLabel(status)}；当前金额是参考价，不是实时售价。</p>{value?.oldest_observed_date && <p className="mt-1 opacity-80">最早观察日期 {value.oldest_observed_date}{value.max_age_days != null ? `，距今 ${value.max_age_days} 天` : ""}</p>}</div>
  </div>;
}
