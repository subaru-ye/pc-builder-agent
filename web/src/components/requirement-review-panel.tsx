"use client";

// 核定面板:展示服务端 review_spec(展开系统默认后的规范化有效需求)的全部
// 有效选型约束。前端只渲染与映射标签,不重算 readiness、默认值或差异;
// schema_version/revision/hash 只存在于请求,不在面板出现。
import { AlertTriangle, Loader2 } from "lucide-react";
import type { RequirementSpec, Session } from "@/lib/api/types";
import { categoryLabels } from "@/lib/domain";
import { Button } from "./ui/button";
import { requirementLabel, requirementValue } from "./requirement-status";

function partsText(parts: { category: keyof typeof categoryLabels; model?: string; quantity?: number }[] | undefined) {
  if (!parts || parts.length === 0) return "没有已有配件";
  return parts.map((part) => `${categoryLabels[part.category] ?? part.category}：${part.model || "型号未知"}${(part.quantity ?? 1) > 1 ? ` × ${part.quantity}` : ""}`).join("、");
}

function Row({ label, value, meta }: { label: string; value: string; meta?: string }) {
  return <div className="flex gap-3 py-2" data-review-row={label}>
    <span className="w-28 shrink-0 text-xs leading-6 text-[var(--ink-muted)]">{label}</span>
    <span className="min-w-0 flex-1 break-words text-sm">{value}{meta && <span className="ml-1 text-xs text-[var(--ink-subtle)]">（{meta}）</span>}</span>
  </div>;
}

function unspecified(value: unknown, fallback = "未指定，本次不作为选型限制") {
  if (value === undefined || value === null || value === "" || Array.isArray(value) && value.length === 0) return fallback;
  return null;
}

export function confirmActionLabel(session: Session) {
  const confirmation = session.requirement_confirmation;
  const relation = session.build_relation;
  if (relation.status === "failed" && confirmation.status === "confirmed") return "按相同需求重新生成";
  if (confirmation.status === "modified") return "确认修改并生成新版本";
  return "确认并开始配置";
}

export function RequirementReviewPanel({ session, busy, error, onConfirm, onBack }: {
  session: Session;
  busy: boolean;
  error: string | null;
  onConfirm: () => void;
  onBack: () => void;
}) {
  const spec = session.review_spec as RequirementSpec | null;
  if (!spec) return null;
  const readiness = session.requirement_readiness;
  const defaults = new Map((readiness?.effective_defaults ?? []).map((item) => [item.field, item.value]));
  const isDefault = (field: string) => defaults.has(field);
  const confirmation = session.requirement_confirmation;
  const diff = confirmation.review_diff;
  const budgetStrengthPrefer = session.requirement_state?.fields["budget_cny"]?.strength === "prefer";
  const gaming = spec.use_case.type === "gaming";
  const observations = spec.requirement_observations ?? [];
  const freeDetails = Object.entries(spec.requirement_details ?? {}).filter(([key]) => key.startsWith("free."));

  return <div className="px-4 py-5 sm:px-6">
    <header>
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-base font-semibold">核定当前需求</h2>
        {confirmation.status === "modified" && <span className="flex items-center gap-1 text-xs status-review"><AlertTriangle size={14} />已修改</span>}
      </div>
      <p className="mt-1 text-sm text-[var(--ink-muted)]">以下是本次配置将使用的全部有效要求（含系统默认）。确认后开始生成主机配置。</p>
    </header>

    {confirmation.status === "modified" && <section className="mt-4 border-l-2 border-l-[var(--review)] pl-3" data-testid="review-diff">
      <h3 className="text-sm font-medium">相对上次确认的变化</h3>
      {diff == null
        ? <p className="mt-1 text-xs text-[var(--ink-muted)]">与上次确认快照的差异暂不可用，请逐项核对以下内容。</p>
        : diff.length === 0
          ? <p className="mt-1 text-xs text-[var(--ink-muted)]">有效需求与上次确认相同。</p>
          : <ul className="mt-2 space-y-1 text-sm">{diff.map((entry) => <li key={entry.field}>{requirementLabel(entry.field)}：{requirementValue(entry.field, entry.before)} → {requirementValue(entry.field, entry.after)}</li>)}</ul>}
    </section>}

    <section className="mt-4 border-t pt-3"><h3 className="text-sm font-medium">核心</h3>
      <Row label="配置范围" value="主机（当前支持）" meta="系统默认" />
      <Row label="预算" value={requirementValue("budget_cny", spec.budget_cny)} meta={isDefault("budget_cny") ? "系统默认" : undefined} />
      <Row label="预算弹性" value={requirementValue("budget_flex", spec.budget_flex)} meta={isDefault("budget_flex") ? "系统默认" : undefined} />
      {session.effective_budget_ceiling_cny != null && <Row label={budgetStrengthPrefer ? "预算参考上沿" : "最高预算"} value={`¥${session.effective_budget_ceiling_cny.toLocaleString("zh-CN")}`} />}
      <Row label="购买范围" value={spec.budget_basis === "full_build" ? "整机参考总价（包含已有件）" : spec.budget_basis === "new_purchase" ? "仅用于新增购买" : "未指定，本次不作为选型限制"} />
      <Row label="复用配件" value={partsText(spec.owned_parts)} />
    </section>

    <section className="mt-3 border-t pt-3"><h3 className="text-sm font-medium">用途</h3>
      <Row label="主要用途" value={requirementValue("use_case.type", spec.use_case.type)} />
      <Row label="游戏 / 应用" value={unspecified(spec.use_case.titles) ?? spec.use_case.titles.join("、")} />
      {gaming && <Row label="分辨率" value={spec.use_case.resolution && spec.use_case.resolution !== "any" ? requirementValue("use_case.resolution", spec.use_case.resolution) : "未指定，本次不作为选型限制"} />}
      <Row label="性能取向" value={requirementValue("use_case.performance_goal", spec.use_case.performance_goal)} meta={isDefault("performance_goal") ? "系统默认" : undefined} />
      <Row label="目标帧率" value={unspecified(spec.use_case.fps_target) ?? `${spec.use_case.fps_target} 帧`} />
    </section>

    <section className="mt-3 border-t pt-3"><h3 className="text-sm font-medium">偏好与约束</h3>
      <Row label="CPU 品牌" value={requirementValue("brand_pref.cpu", spec.brand_pref?.cpu ?? "any")} meta={isDefault("brand_pref.cpu") ? "系统默认" : undefined} />
      <Row label="显卡品牌" value={requirementValue("brand_pref.gpu", spec.brand_pref?.gpu ?? "any")} meta={isDefault("brand_pref.gpu") ? "系统默认" : undefined} />
      <Row label="静音" value={requirementValue("noise_pref", spec.noise_pref)} meta={isDefault("noise_pref") ? "系统默认" : undefined} />
      <Row label="尺寸" value={requirementValue("size_pref", spec.size_pref)} meta={isDefault("size_pref") ? "系统默认" : undefined} />
      {(spec.priority ?? []).length > 0 && <Row label="优先投入" value={spec.priority!.map((category) => categoryLabels[category] ?? category).join("、")} />}
      <Row label="补充说明" value={unspecified(spec.notes, "无") ?? spec.notes} />
      {Object.entries(spec.requirement_details ?? {}).filter(([key]) => key !== "free." && !key.startsWith("free.")).map(([key, value]) => <Row key={key} label={requirementLabel(key)} value={requirementValue(key, value)} />)}
      {freeDetails.map(([key, value]) => <Row key={key} label="补充要求" value={String(value)} />)}
    </section>

    {observations.length > 0 && <section className="mt-3 border-t pt-3"><h3 className="text-sm font-medium">未纳入本次配置</h3><ul className="mt-2 space-y-1 text-sm text-[var(--ink-muted)]">{observations.map((item, index) => <li key={index}>存在歧义，本次未采用：{item.text}</li>)}</ul></section>}

    <p className="mt-4 border-t pt-3 text-xs leading-5 text-[var(--ink-subtle)]">当前仅生成主机八件（处理器、显卡、主板、内存、固态硬盘、电源、机箱、散热器）；确认后如需修改需求，需要重新核定并生成新版本。</p>

    {error && <p role="alert" className="mt-3 text-sm status-fail">{error}</p>}
    <div className="mt-4 flex flex-wrap items-center justify-end gap-2 border-t pt-4">
      <Button variant="ghost" onClick={onBack}>返回修改</Button>
      <Button disabled={busy} onClick={onConfirm} data-testid="review-confirm">
        {busy && <Loader2 className="animate-spin" size={15} />}{confirmActionLabel(session)}
      </Button>
    </div>
  </div>;
}
