import type { Metadata } from "next";
import { AlertTriangle, CheckCircle2, CircleHelp, XCircle } from "lucide-react";
import { notFound } from "next/navigation";
import { PublicShareActions } from "@/components/public-share-actions";
import { freshnessLabel, PriceFreshnessNotice } from "@/components/price-freshness";
import { PriceAvailabilityNotice } from "@/components/price-availability";
import { configuredWebBaseURL, fetchPublicShare } from "@/lib/api/server";
import { categories, categoryLabels, ruleLabels, ruleOrder, statusLabel } from "@/lib/domain";

export const dynamic = "force-dynamic";

export async function generateMetadata({ params }: PageProps<"/share/[token]">): Promise<Metadata> {
  const { token } = await params;
  const build = await fetchPublicShare(token);
  if (!build) return { title: "分享链接不可用", robots: { index: false, follow: false } };
  const title = `装机配置单 v${build.summary.version} · ¥${build.quote.total_cny}`;
  const description = `${caseLabel(build.requirement.use_case.type)}配置，价格快照 ${build.quote.snapshot_date}`;
  const canonical = new URL(`/share/${token}`, configuredWebBaseURL());
  return {
    metadataBase: configuredWebBaseURL(), title, description,
    alternates: { canonical }, robots: { index: false, follow: false, nocache: true },
    openGraph: { title, description, type: "article", url: canonical, images: [{ url: `${canonical.pathname}/image`, width: 1200, height: 630, alt: title }] },
  };
}

export default async function PublicSharePage({ params }: PageProps<"/share/[token]">) {
  const { token } = await params;
  const build = await fetchPublicShare(token);
  if (!build) notFound();
  const known = (key: string) => !build.requirement.known_fields || build.requirement.known_fields.includes(key);
  const byCategory = new Map(build.parts.map((part) => [part.category, part]));
  const byRule = new Map(build.validation.checks.map((check) => [check.rule_id, check]));
  return <main className="public-share min-h-screen bg-[var(--canvas)]">
    <header className="border-b bg-[var(--surface-1)]"><div className="mx-auto flex min-h-16 max-w-6xl items-center justify-between px-4 sm:px-8"><span className="font-semibold">装机配置单 Agent</span><span className="text-sm text-[var(--ink-muted)]">只读分享</span></div></header>
    <div className="mx-auto max-w-6xl px-4 py-8 sm:px-8 sm:py-12">
      <section className="border-b pb-8"><div className="flex flex-col justify-between gap-6 md:flex-row md:items-end"><div><p className="text-sm text-[var(--ink-subtle)]">{build.summary.intent_label} · v{build.summary.version}</p><h1 className="mt-2 text-3xl font-semibold tracking-tight sm:text-4xl">¥{build.quote.total_cny} 的装机配置</h1><p className="mt-3 text-[var(--ink-muted)]">{caseLabel(build.requirement.use_case.type)} · 价格快照 {build.quote.snapshot_date} · 创建于 {formatDate(build.share.created_at)}</p></div><OverallStatus value={build.summary.overall_status} /></div><div className="mt-5"><PriceFreshnessNotice value={build.quote.price_freshness} /></div><div className="mt-6"><PublicShareActions token={token} version={build.summary.version} /></div></section>

      <section className="border-b py-8" aria-labelledby="public-requirement"><h2 id="public-requirement" className="text-lg font-semibold">需求摘要</h2><dl className="mt-5 grid grid-cols-2 gap-x-6 gap-y-5 sm:grid-cols-4"><Meta label="预算" value={known("budget_cny") ? `¥${build.requirement.budget_cny}` : "未说明"} /><Meta label="预算弹性" value={known("budget_flex") ? `${build.requirement.budget_flex_percent}%` : "未说明"} /><Meta label="用途" value={caseLabel(build.requirement.use_case.type)} /><Meta label="目标分辨率" value={build.requirement.use_case.resolution ?? "—"} /><Meta label="目标帧率" value={build.requirement.use_case.fps_target ? `${build.requirement.use_case.fps_target} FPS` : "—"} /><Meta label="尺寸偏好" value={known("size_pref") ? build.requirement.size_pref.toUpperCase() : "未说明"} /><Meta label="噪音偏好" value={preferenceLabel(build.requirement.noise_pref)} /><Meta label="品牌偏好" value={`CPU ${known("brand_pref.cpu") ? build.requirement.brand_pref.cpu.toUpperCase() : "未说明"} / GPU ${known("brand_pref.gpu") ? build.requirement.brand_pref.gpu.toUpperCase() : "未说明"}`} /></dl>{build.requirement.use_case.titles.length > 0 && <p className="mt-5 text-sm"><span className="text-[var(--ink-subtle)]">目标应用 / 游戏：</span>{build.requirement.use_case.titles.join("、")}</p>}</section>

      <section className="border-b py-8" aria-labelledby="public-parts"><div className="flex items-end justify-between gap-4"><div><h2 id="public-parts" className="text-lg font-semibold">配置清单</h2><p className="mt-1 text-sm text-[var(--ink-muted)]">八类核心配件及选择理由</p></div><div className="text-right"><div className="text-xl font-semibold tabular">¥{build.quote.total_cny}</div><div className="text-xs text-[var(--ink-subtle)]">{build.quote.budget_known === false ? "预算未说明" : `预算差 ¥${build.quote.budget_delta_cny}`}</div></div></div><div className="mt-5 border-y">{categories.map((category) => { const part = byCategory.get(category); return <div key={category} className="grid grid-cols-[76px_minmax(0,1fr)] gap-3 border-b py-4 last:border-b-0 sm:grid-cols-[100px_minmax(0,1fr)_140px]"><div className="text-xs font-medium text-[var(--ink-subtle)]">{categoryLabels[category]}</div><div className="min-w-0"><div className="font-medium">{part?.name || "未选择"}</div><div className="mt-1 font-mono text-xs text-[var(--ink-subtle)]">{part?.sku || "—"}{part && part.quantity > 1 ? ` × ${part.quantity}` : ""}</div>{part?.rationale && <p className="mt-2 text-sm text-[var(--ink-muted)]">{part.rationale}</p>}{part?.price_observed_date && <p className={`mt-2 text-xs ${part.price_freshness === "stale" ? "status-fail" : part.price_freshness === "aging" || part.price_freshness === "unknown" ? "status-review" : "text-[var(--ink-subtle)]"}`}>参考价观察于 {part.price_observed_date} · {freshnessLabel(part.price_freshness)}</p>}<PriceAvailabilityNotice value={part?.price_availability_basis} /></div><div className="col-start-2 text-left sm:col-auto sm:text-right">{part?.subtotal_cny ? <span className="tabular">¥{part.subtotal_cny}</span> : <span className="text-sm status-review">缺价，未计入合计</span>}</div></div>; })}</div></section>

      {!!build.sources?.length && <details className="border-b py-6"><summary className="cursor-pointer font-medium">资料来源</summary><ul className="mt-4 space-y-3 text-sm">{build.sources.filter(e => e.url.startsWith("https://")).map((e,i) => <li key={i}><a className="break-words underline" href={e.url} target="_blank" rel="noopener noreferrer">{e.title || "商品资料"}</a><p className="mt-1 text-xs text-[var(--ink-muted)]">获取于 {e.captured_at.slice(0,10)}</p></li>)}</ul></details>}
      <section className="border-b py-8" aria-labelledby="public-validation"><div className="flex items-center justify-between"><div><h2 id="public-validation" className="text-lg font-semibold">兼容性校验</h2><p className="mt-1 text-sm text-[var(--ink-muted)]">固定 12 条规则的保存结果</p></div><OverallStatus value={build.validation.overall_status} compact /></div><div className="mt-5 divide-y border-y">{ruleOrder.map((rule) => { const check = byRule.get(rule); const visual = check ? check.outcome === "pass" ? "pass" : check.severity === "error" ? "fail" : check.outcome === "unknown" ? "unknown" : "review" : "unknown"; return <div key={rule} className="flex gap-3 py-4"><StatusIcon value={visual} /><div className="min-w-0 flex-1"><div className="flex justify-between gap-3"><span className="font-medium">{ruleLabels[rule]}</span><span className={`text-xs status-${visual}`}>{statusLabel(visual)}</span></div><p className="mt-1 text-sm text-[var(--ink-muted)]">{check?.detail || "数据不足"}</p>{check?.missing_fields.length ? <p className="mt-1 text-xs text-[var(--ink-subtle)]">缺少字段：{check.missing_fields.join("、")}</p> : null}</div></div>; })}</div></section>

      <section className="py-8" aria-labelledby="public-notice"><h2 id="public-notice" className="text-sm font-semibold">购买前说明</h2><ul className="mt-3 list-disc space-y-2 pl-5 text-sm text-[var(--ink-muted)]">{build.disclaimers.map((item) => <li key={item}>{item}</li>)}</ul></section>
    </div>
  </main>;
}

function Meta({ label, value }: { label: string; value: string }) { return <div><dt className="text-xs text-[var(--ink-subtle)]">{label}</dt><dd className="mt-1 text-sm">{value}</dd></div>; }
function caseLabel(value: string) { return value === "unknown" ? "用途未说明" : value === "gaming" ? "游戏" : value === "productivity" ? "生产力" : "通用"; }
function preferenceLabel(value: string) { return value === "unknown" ? "未说明" : value === "silent" ? "安静" : value === "normal" ? "正常" : "不限"; }
function formatDate(value: string) { return new Intl.DateTimeFormat("zh-CN", { year: "numeric", month: "2-digit", day: "2-digit" }).format(new Date(value)); }
function OverallStatus({ value, compact = false }: { value: string; compact?: boolean }) { const visual = value === "pass" ? "pass" : value === "fail" ? "fail" : "review"; return <div className={`flex items-center gap-2 ${compact ? "text-sm" : "text-base"} status-${visual}`}><StatusIcon value={visual} />{statusLabel(visual)}</div>; }
function StatusIcon({ value }: { value: string }) { const Icon = value === "pass" ? CheckCircle2 : value === "fail" ? XCircle : value === "review" ? AlertTriangle : CircleHelp; return <Icon aria-hidden className={`shrink-0 status-${value}`} size={18} />; }
