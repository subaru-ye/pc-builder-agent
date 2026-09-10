"use client";

import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, CheckCircle2, CircleHelp, GitCompareArrows, XCircle } from "lucide-react";
import { useRouter, useSearchParams } from "next/navigation";
import type { ReactNode } from "react";
import { api } from "@/lib/api/client";
import { queryKeys } from "@/lib/api/query-keys";
import type { BuildSummary, BuildView, PartCategory } from "@/lib/api/types";
import { categories, categoryLabels, ruleLabels, ruleOrder, statusLabel } from "@/lib/domain";
import { useUIStore } from "@/stores/ui";
import { Button } from "./ui/button";
import { freshnessLabel, PriceFreshnessNotice } from "./price-freshness";
import { PriceAvailabilityNotice } from "./price-availability";

const tabs = [
  ["requirement", "当前需求"], ["build", "配置"], ["validation", "校验"], ["versions", "版本"],
] as const;

export function BuildInspector({ sessionID, builds, build, latestVersion, canChange, onReplace, requirementPanel, requirementSummary }: {
  sessionID: string;
  builds: BuildSummary[];
  build?: BuildView;
  latestVersion: number | null;
  canChange: boolean;
  onReplace: (category: PartCategory) => void;
  requirementPanel?: ReactNode;
  requirementSummary?: ReactNode;
}) {
  const router = useRouter();
  const search = useSearchParams();
  const tab = useUIStore((state) => state.inspectorTab);
  const setTab = useUIStore((state) => state.setInspectorTab);
  const diffFrom = useUIStore((state) => state.diffFrom);
  const diffTo = useUIStore((state) => state.diffTo);
  const setDiff = useUIStore((state) => state.setDiff);
  const version = Number(search.get("version")) || latestVersion;
  const from = diffFrom ?? (build?.summary.parent_version ?? builds.at(-2)?.version ?? 1);
  const to = diffTo ?? (version ?? latestVersion ?? 1);
  const diff = useQuery({
    queryKey: queryKeys.diff(sessionID, from, to), queryFn: () => api.getDiff(sessionID, from, to),
    enabled: tab === "versions" && builds.length > 1 && from !== to,
  });
  const chooseVersion = (next: number) => {
    const params = new URLSearchParams(search.toString());
    params.set("version", String(next));
    router.replace(`?${params.toString()}`, { scroll: false });
  };

  if (!build && !requirementPanel) return <EmptyInspector />;
  const activeTab = !build ? "requirement" : tab;
  const historical = build && latestVersion !== null && build.summary.version !== latestVersion;
  return <section className="flex h-full min-h-0 flex-col bg-[var(--surface-1)]" aria-label="配置检查器">
    {activeTab !== "requirement" && requirementSummary}
    {build && activeTab !== "requirement" && <div className="border-b px-4 py-4 sm:px-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div><div className="text-xs text-[var(--ink-subtle)]">当前查看</div><div className="mt-1 flex items-center gap-2"><span className="text-lg font-semibold">v{build.summary.version}</span>{historical && <span className="rounded border px-2 py-0.5 text-xs text-[var(--ink-muted)]">历史只读</span>}<StatusMark value={build.summary.overall_status} /></div></div>
        <div className="text-right"><div className="tabular text-xl font-semibold">¥{build.quote.budget_basis === "new_purchase" ? build.quote.purchase_total_cny ?? build.quote.total_cny : build.quote.total_cny}</div>{build.quote.purchase_total_cny != null && <div className="text-xs text-[var(--ink-muted)]">新增购买 ¥{build.quote.purchase_total_cny} · 整机参考 ¥{build.quote.total_cny}</div>}<div className="text-xs text-[var(--ink-muted)]">预算差 ¥{build.quote.budget_delta_cny}</div></div>
      </div>
      <dl className="mt-4 grid grid-cols-3 gap-3 text-xs"><Meta label="父版本" value={build.summary.parent_version ? `v${build.summary.parent_version}` : "—"} /><Meta label="价格快照" value={build.quote.snapshot_date} /><Meta label="缺价项" value={String(build.quote.missing_count)} /></dl>
      <div className="mt-4"><PriceFreshnessNotice value={build.quote.price_freshness} compact /></div>
    </div>}
    <div className="flex min-h-11 overflow-x-auto border-b px-2" role="tablist" aria-label="检查器页面">
      {tabs.map(([key, label]) => <button key={key} role="tab" aria-selected={activeTab === key} disabled={!build && key !== "requirement"} className={`min-h-11 shrink-0 border-b-2 px-4 text-sm disabled:opacity-40 ${activeTab === key ? "border-b-[var(--primary)] text-[var(--ink)]" : "border-b-transparent text-[var(--ink-muted)]"}`} onClick={() => setTab(key)}>{label}</button>)}
    </div>
    <div className="min-h-0 flex-1 overflow-y-auto" tabIndex={0} aria-label="检查器内容">
      {activeTab === "build" && build && <Parts build={build} allowReplace={canChange && !historical} onReplace={onReplace} />}
      {activeTab === "validation" && build && <Validation build={build} />}
      {activeTab === "requirement" && (requirementPanel ?? (build && <RequirementReadOnly build={build} />))}
      {activeTab === "versions" && build && <><Versions builds={builds} selected={build.summary.version} onSelect={chooseVersion} from={from} to={to} onDiff={setDiff} diff={diff.data} disclaimers={build.disclaimers} /><details className="border-t"><summary className="cursor-pointer px-4 py-4 text-sm sm:px-6">查看 v{build.summary.version} 的已确认需求（只读）</summary><RequirementReadOnly build={build} /></details></>}
    </div>
  </section>;
}

function EmptyInspector() {
  return <section className="flex h-full items-center justify-center bg-[var(--surface-1)] p-8 text-center"><div><CircleHelp className="mx-auto text-[var(--ink-subtle)]" /><h2 className="mt-4 font-medium">配置尚未生成</h2><p className="mt-1 max-w-sm text-sm text-[var(--ink-muted)]">需求确认后，零件、价格、校验和版本记录会在这里集中展示。</p></div></section>;
}

function Parts({ build, allowReplace, onReplace }: { build: BuildView; allowReplace: boolean; onReplace: (category: PartCategory) => void }) {
  const byCategory = new Map(build.parts.map((part) => [part.category, part]));
  return <div>
    {categories.map((category) => { const part = byCategory.get(category); return <div key={category} className="grid grid-cols-[88px_minmax(0,1fr)_auto] gap-3 border-b px-4 py-4 sm:px-6">
      <div className="text-xs font-medium text-[var(--ink-subtle)]">{categoryLabels[category]}</div>
      <div className="min-w-0"><div className="font-medium">{part?.name ?? "未选择"}</div>{part?.rationale && <p className="mt-1 text-xs text-[var(--ink-muted)]">{part.rationale}</p>}{part?.price_observed_date && <p className={`mt-1 text-xs ${part.price_freshness === "stale" ? "status-fail" : part.price_freshness === "aging" || part.price_freshness === "unknown" ? "status-review" : "text-[var(--ink-subtle)]"}`}>观察于 {part.price_observed_date} · {freshnessLabel(part.price_freshness)}</p>}<PriceAvailabilityNotice value={part?.price_availability_basis} />{part?.quantity && part.quantity > 1 ? <span className="text-xs text-[var(--ink-subtle)]">数量 × {part.quantity}</span> : null}</div>
      <div className="text-right"><div className="tabular text-sm">{part?.subtotal_cny ? `¥${part.subtotal_cny}` : <span className="status-review">缺价，未计入合计</span>}</div>{part?.owned && <div className="text-xs text-[var(--ink-muted)]">用户已有，无需购买</div>}{allowReplace && !part?.owned && <Button variant="ghost" size="sm" className="mt-1" onClick={() => onReplace(category)}>更换此件</Button>}</div>
    </div>; })}
    <Disclaimers values={build.disclaimers} />
  </div>;
}

function Validation({ build }: { build: BuildView }) {
  const byRule = new Map(build.validation.checks.map((check) => [check.rule_id, check]));
  return <div>{ruleOrder.map((id) => { const check = byRule.get(id); const visual = check ? check.outcome === "pass" ? "pass" : check.severity === "error" ? "fail" : check.outcome === "unknown" ? "unknown" : "review" : "unknown"; return <div key={id} className="flex gap-3 border-b px-4 py-3 sm:px-6"><StatusIcon value={visual} /><div className="min-w-0 flex-1"><div className="flex justify-between gap-3"><span className="font-medium">{ruleLabels[id]}</span><span className={`text-xs status-${visual}`}>{statusLabel(visual)}</span></div><p className="mt-1 text-xs text-[var(--ink-muted)]">{check?.detail ?? "后端未返回此规则的数据。"}</p></div></div>; })}<Disclaimers values={build.disclaimers} /></div>;
}

function RequirementReadOnly({ build }: { build: BuildView }) {
  const r = build.requirement;
  return <div className="space-y-5 p-4 sm:p-6"><dl className="grid grid-cols-2 gap-4 text-sm"><Meta label="预算" value={`¥${r.budget_cny}`} /><Meta label="弹性" value={`${Math.round(r.budget_flex * 100)}%`} /><Meta label="用途" value={r.use_case.type} /><Meta label="尺寸" value={r.size_pref} /><Meta label="噪音" value={r.noise_pref} /><Meta label="分辨率" value={r.use_case.resolution ?? "—"} /></dl>{r.use_case.titles.length > 0 && <div><div className="text-xs text-[var(--ink-subtle)]">目标应用 / 游戏</div><p className="mt-1">{r.use_case.titles.join("、")}</p></div>}<div><div className="text-xs text-[var(--ink-subtle)]">补充说明</div><p className="mt-1 whitespace-pre-wrap text-[var(--ink-muted)]">{r.notes || "无"}</p></div><Disclaimers values={build.disclaimers} /></div>;
}

function Versions({ builds, selected, onSelect, from, to, onDiff, diff, disclaimers }: { builds: BuildSummary[]; selected: number; onSelect: (v: number) => void; from: number; to: number; onDiff: (a: number, b: number) => void; diff?: Awaited<ReturnType<typeof api.getDiff>>; disclaimers: string[] }) {
  return <div className="p-4 sm:p-6"><ol className="border-l border-l-[var(--hairline-strong)] pl-4">{builds.map((item) => <li key={item.version} className="relative pb-5"><span className={`absolute -left-[21px] top-1 h-2.5 w-2.5 rounded-full ${item.version === selected ? "bg-[var(--primary)]" : "bg-[var(--hairline-strong)]"}`} /><button className="w-full text-left" onClick={() => onSelect(item.version)}><span className="font-medium">v{item.version}</span><span className="ml-2 text-xs text-[var(--ink-subtle)]">{item.parent_version ? `来自 v${item.parent_version}` : "初始版本"}</span><p className="mt-1 text-sm text-[var(--ink-muted)]">{item.intent}</p></button></li>)}</ol>
    {builds.length > 1 && <div className="mt-2 border-t pt-5"><div className="mb-3 flex items-center gap-2 font-medium"><GitCompareArrows size={16} />版本对比</div><div className="grid grid-cols-2 gap-2"><VersionSelect label="来源" value={from} builds={builds} onChange={(v) => onDiff(v, to)} /><VersionSelect label="目标" value={to} builds={builds} onChange={(v) => onDiff(from, v)} /></div>{from === to ? <p className="mt-4 text-sm text-[var(--ink-muted)]">请选择两个不同版本。</p> : diff && <div className="mt-4">{diff.snapshot_warning && <p className="mb-3 rounded-md border border-[var(--review)]/40 bg-[var(--review)]/10 p-3 text-sm status-review"><AlertTriangle className="mr-2 inline" size={15} />{diff.snapshot_warning}</p>}<p className="mb-3 text-sm">总价变化 <span className="tabular font-medium">¥{diff.total_delta_cny}</span></p>{categories.map((category) => { const line = diff.lines.find((item) => item.category === category); return <div key={category} className={`grid grid-cols-[76px_1fr] gap-2 border-t py-3 text-sm ${line?.changed ? "bg-[var(--primary)]/5" : ""}`}><span className="text-[var(--ink-subtle)]">{categoryLabels[category]}</span><div><div>{line?.before || "—"} → {line?.after || "—"}</div>{line?.price_delta_cny && <div className="mt-1 text-xs text-[var(--ink-muted)]">价格变化 ¥{line.price_delta_cny}</div>}</div></div>; })}</div>}</div>}
    <Disclaimers values={disclaimers} />
  </div>;
}

function VersionSelect({ label, value, builds, onChange }: { label: string; value: number; builds: BuildSummary[]; onChange: (v: number) => void }) { return <label className="text-xs text-[var(--ink-muted)]">{label}<select aria-label={label} className="mt-1 h-11 w-full rounded-md border bg-[var(--canvas)] px-3 text-[var(--ink)]" value={value} onChange={(e) => onChange(Number(e.target.value))}>{builds.map((item) => <option value={item.version} key={item.version}>v{item.version}</option>)}</select></label>; }
function Meta({ label, value }: { label: string; value: string }) { return <div><dt className="text-[var(--ink-subtle)]">{label}</dt><dd className="mt-0.5 tabular text-[var(--ink-muted)]">{value}</dd></div>; }
function StatusMark({ value }: { value: string }) { return <span className={`flex items-center gap-1 text-xs status-${value === "review" ? "review" : value}`}><StatusIcon value={value} />{statusLabel(value)}</span>; }
function StatusIcon({ value }: { value: string }) { const C = value === "pass" ? CheckCircle2 : value === "fail" ? XCircle : value === "review" ? AlertTriangle : CircleHelp; return <C aria-hidden size={16} className={`shrink-0 status-${value}`} />; }
function Disclaimers({ values }: { values: string[] }) { return <div className="border-t p-4 text-xs text-[var(--ink-subtle)] sm:p-6"><div className="mb-2 font-medium text-[var(--ink-muted)]">购买前说明</div><ul className="list-disc space-y-1 pl-4">{values.map((item, index) => <li key={`${index}-${item}`}>{item}</li>)}</ul></div>; }
