"use client";

import { useState } from "react";
import { ArrowUpRight, Check, CircleHelp, History, Loader2, Pencil, RotateCcw, X } from "lucide-react";
import type { RequirementOperation, RequirementState, Session } from "@/lib/api/types";
import { categories, categoryLabels, phaseLabels } from "@/lib/domain";
import { userMessage } from "@/lib/api/problem";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Textarea } from "./ui/textarea";

type Field = RequirementState["fields"][string];
type Source = NonNullable<Field["source"]>;

const labels: Record<string, string> = {
  budget_cny: "预算", budget_basis: "预算口径", budget_flex: "预算弹性",
  "use_case.type": "主要用途", "use_case.titles": "游戏 / 应用", "use_case.resolution": "分辨率", "use_case.fps_target": "目标帧率", "use_case.performance_goal": "性能取向",
  existing_parts: "已有配件类别", owned_parts: "已有配件型号", "brand_pref.cpu": "CPU 品牌", "brand_pref.gpu": "显卡品牌",
  noise_pref: "静音", appearance: "外观", size_pref: "尺寸", notes: "补充说明", recipient: "装机对象", priority: "优先投入",
};
const orderedFields = Object.keys(labels);
export function requirementLabel(key: string) { return labels[key] ?? (key.startsWith("free.") ? "补充要求" : key); }
const kindLabels = { fact: "用途事实", context: "补充说明", constraint: "配置条件" };
const capabilityLabels: Record<string, string> = { monitor: "显示器", keyboard: "键盘", mouse: "鼠标" };

function fieldMeaning(name: string, field: Field) {
  // Old records without a meaning stay unclassified; factual field names already
  // identify their role, so a legacy must flag adds no useful presentation.
  if (!field.kind && ["use_case.type", "use_case.titles", "recipient", "existing_parts", "owned_parts"].includes(name)) return null;
  if (name === "notes" && field.kind === "context") return null;
  if (field.kind === "fact" || field.kind === "context") return kindLabels[field.kind];
  return field.strength === "must" ? "必须满足" : field.strength === "prefer" ? "尽量满足" : "强度未说明";
}
const choices: Record<string, [string, string][]> = {
  budget_basis: [["new_purchase", "仅用于新增购买"], ["full_build", "整机参考总价（包含已有件）"]],
  "use_case.type": [["gaming", "游戏"], ["productivity", "生产力"], ["general", "日常综合"]],
  "use_case.resolution": [["1080p", "1080p"], ["2K", "2K"], ["4K", "4K"]],
  "use_case.performance_goal": [["balanced", "均衡"], ["fps_first", "帧率优先"], ["quality_first", "画质优先"]],
  "brand_pref.cpu": [["any", "不限"], ["amd", "AMD"], ["intel", "Intel"]],
  "brand_pref.gpu": [["any", "不限"], ["amd", "AMD"], ["nvidia", "NVIDIA"]],
  noise_pref: [["any", "不限"], ["silent", "安静"], ["normal", "常规"]],
  size_pref: [["any", "不限"], ["atx", "ATX"], ["matx", "M-ATX"], ["itx", "ITX"]],
};
const selectClass = "h-11 w-full rounded-md border bg-[var(--canvas)] px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--primary)]";

export function requirementValue(field: string, value: unknown): string {
  if (value === undefined || value === null) return "未知";
  if (field === "budget_cny") return `¥${Number(value).toLocaleString("zh-CN")}`;
  if (field === "budget_flex") return Number(value) === 0 ? "严格预算" : `最多上浮 ${Math.round(Number(value) * 100)}%`;
  if (field === "use_case.fps_target") return `${value} 帧`;
  if (field === "recipient") return ({ self: "自己", friend: "朋友", other: "他人" } as Record<string, string>)[String(value)] ?? String(value);
  const choice = choices[field]?.find(([key]) => key === value);
  if (choice) return choice[1];
  if (Array.isArray(value)) {
    if (value.length === 0) return field === "existing_parts" || field === "owned_parts" ? "没有已有配件" : "无";
    return value.map((item) => {
      if (typeof item === "object" && item !== null && "category" in item) {
        const part = item as { category: keyof typeof categoryLabels; model?: string; quantity?: number };
        return `${categoryLabels[part.category] ?? part.category}：${part.model || "型号未知"}${(part.quantity ?? 1) > 1 ? ` × ${part.quantity}` : ""}`;
      }
      return categoryLabels[item as keyof typeof categoryLabels] ?? String(item);
    }).join("、");
  }
  return typeof value === "object" ? JSON.stringify(value) : String(value);
}

function sourceLabel(source: Source | undefined) {
  if (!source) return null;
  return source.kind === "edit" ? "手动修改" : source.kind === "confirmed" ? "来自已确认需求" : "来自对话";
}

/** 需求就绪度的一个词(服务端三轴/readiness 的直读,不含前端业务判断)。 */
function headlineStatus(session: Session) {
  const missing = session.requirement_readiness?.missing_fields.length ?? 0;
  if (missing > 0) return `还缺 ${missing} 项`;
  if (session.requirement_confirmation.status === "confirmed") return "已确认";
  if (session.requirement_confirmation.status === "modified") return "已修改";
  return "可以核定";
}

/** 聊天顶部摘要:阶段/三轴可读摘要 + 缺失数量 + 打开右栏需求状态,不重复字段表。 */
export function RequirementSummary({ session, onOpen }: { session: Session; onOpen: () => void }) {
  const missing = session.requirement_readiness?.missing_fields.length ?? 0;
  const eligible = session.requirement_readiness?.confirmation_eligible ?? false;
  const readiness = !session.requirement_readiness ? null : missing > 0 ? `还缺 ${missing} 项` : eligible ? "可以核定" : "等待核对";
  return <div aria-label="需求摘要" className="px-4 py-2 sm:px-6"><div className="flex items-center justify-between gap-3">
    <div className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1 text-xs text-[var(--ink-muted)]">
      <span>{session.status_label || phaseLabels[session.phase]}</span>
      {readiness && <span aria-label="需求进度">{readiness}</span>}
    </div>
    <Button variant="ghost" size="sm" className="min-h-11 shrink-0 lg:min-h-8" aria-label="打开需求状态" onClick={onOpen}>需求状态<ArrowUpRight size={14} /></Button>
  </div></div>;
}

// 字段 → 展示分组;仅是呈现归类,不参与任何业务判定。
const groupOf: Record<string, "core" | "usage" | "reuse" | "prefs"> = {
  budget_cny: "core", budget_basis: "core", "use_case.type": "core",
  "use_case.titles": "usage", "use_case.resolution": "usage", "use_case.performance_goal": "usage", "use_case.fps_target": "usage",
  existing_parts: "reuse", owned_parts: "reuse",
  "brand_pref.cpu": "prefs", "brand_pref.gpu": "prefs", noise_pref: "prefs", size_pref: "prefs",
  appearance: "prefs", recipient: "prefs", priority: "prefs", notes: "prefs",
};
const groupTitles: Record<string, string> = { core: "核心需求", usage: "用途需求", reuse: "复用配件", prefs: "可选偏好" };

type RowKey = string;

export function RequirementStatusPane({ session, busy, onUpdate, onSource, onOpenReview, onOpenEditor }: {
  session: Session;
  busy: boolean;
  onUpdate: (operations: RequirementOperation[]) => Promise<void>;
  onSource: (messageID: string) => void;
  onOpenReview: () => void;
  onOpenEditor: () => void;
}) {
  const [editing, setEditing] = useState<string | null>(null);
  const [expandedSource, setExpandedSource] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const state = session.requirement_state;
  if (!state) return null;
  const readiness = session.requirement_readiness;
  const missing = readiness?.missing_fields ?? [];
  const defaults = new Map((readiness?.effective_defaults ?? []).map((item) => [item.field, item.value]));
  const conflicts = new Set([...(readiness?.blocking_conflicts ?? []), ...Object.keys(state.fields).filter((key) => state.fields[key].status === "conflict")]);
  const locked = busy || saving;

  const update = async (operation: RequirementOperation) => {
    setSaving(true); setError(null);
    try { await onUpdate([operation]); setEditing(null); return true; }
    catch (cause) { setError(userMessage(cause)); return false; }
    finally { setSaving(false); }
  };

  // 已知字段 = 状态出现过的 + readiness 判定缺失的 + 被系统默认展开的(预算弹性
  // 只出现在预算块;configuration_scope 是范围声明,不是需求字段行)。
  const known = new Set<RowKey>([...Object.keys(state.fields), ...missing, ...defaults.keys()]);
  known.delete("budget_flex");
  known.delete("configuration_scope");
  const activeCount = Object.values(state.fields).filter((field) => field.status === "active").length;
  const activeAny = (key: string) => state.fields[key]?.status === "active" && (state.fields[key].value === "any");
  const rowState = (key: string): "active" | "conflict" | "removed" | "requiredUnknown" | "optionalUnknown" | "default" => {
    const field = state.fields[key];
    if (field?.status === "conflict" || conflicts.has(key)) return "conflict";
    if (field?.status === "removed") return "removed";
    if (field?.status === "active") return "active";
    if (missing.includes(key)) return "requiredUnknown";
    // 真实状态里所有已知字段都以 unknown 键存在:有效系统默认必须在
    // unknown 兜底之前判定,否则默认行会被误标为"未指定"。
    if (defaults.has(key)) return "default";
    return "optionalUnknown";
  };
  const rowValue = (key: string) => {
    const field = state.fields[key];
    if (field && ["active", "conflict"].includes(field.status)) return field.value;
    if (defaults.has(key)) return defaults.get(key);
    return undefined;
  };

  const rows = [...known].map((key) => ({
    key,
    group: groupOf[key] ?? "prefs",
    state: rowState(key),
  })).sort((a, b) => orderedFields.indexOf(a.key) - orderedFields.indexOf(b.key));

  const renderRow = ({ key, state: rowStatus }: { key: RowKey; state: ReturnType<typeof rowState> }) => {
    const field = state.fields[key];
    const value = rowValue(key);
    const meaning = field ? fieldMeaning(key, field) : null;
    const isSystemDefault = rowStatus === "default";
    // 撤销墓碑不清除系统默认:有效投影仍按默认执行,须诚实展示而不是"未指定"。
    const removedBadge = defaults.has(key) ? "已撤销，当前按系统默认" : "已撤销，当前未指定";
    const stateBadge = rowStatus === "conflict" ? <span className="text-xs status-review">需确认</span>
      : rowStatus === "removed" ? <span className="text-xs text-[var(--ink-subtle)]">{removedBadge}</span>
      : rowStatus === "requiredUnknown" ? <span className="text-xs text-[var(--ink-subtle)]">待填写</span>
      : rowStatus === "optionalUnknown" ? <span className="text-xs text-[var(--ink-subtle)]">未指定</span>
      : isSystemDefault ? <span className="text-xs text-[var(--ink-subtle)]">系统默认</span>
      : null;
    const valueText = rowStatus === "removed" && !defaults.has(key) ? null
      : rowStatus === "requiredUnknown" || rowStatus === "optionalUnknown" ? null
      : activeAny(key) && !isSystemDefault ? "不限（用户已确认）"
      : value !== undefined ? requirementValue(key, value) : null;
    const meta = [
      !isSystemDefault ? sourceLabel(field?.source) : null,
      field && rowStatus === "active" ? meaning : null,
      field?.scope === "temporary" ? "临时例外" : null,
    ].filter(Boolean) as string[];
    return <div key={key} className="py-3" data-field-row={key}>
      <div className="flex items-center gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <span className="text-xs text-[var(--ink-muted)]">{requirementLabel(key)}</span>
            {stateBadge}
            {meta.length > 0 && <span className="text-xs text-[var(--ink-subtle)]">{meta.join(" · ")}</span>}
          </div>
          {valueText && <p className={`mt-1 break-words whitespace-pre-wrap text-sm ${rowStatus === "optionalUnknown" ? "text-[var(--ink-subtle)]" : ""}`}>{valueText}</p>}
          {rowStatus === "conflict" && field?.source?.quote && <p className="mt-1 text-xs text-[var(--ink-muted)]">原话：{field.source.quote}</p>}
        </div>
        <div className="flex shrink-0 items-center gap-1">
          {rowStatus !== "removed" && <Button variant="ghost" size="sm" aria-label={`${rowStatus === "requiredUnknown" ? "补充" : "修改"}${requirementLabel(key)}`} disabled={locked} onClick={() => setEditing(editing === key ? null : key)}><Pencil size={14} /><span>{rowStatus === "requiredUnknown" ? "补充" : "修改"}</span></Button>}
          <Button variant="ghost" size="sm" aria-label={`查看${requirementLabel(key)}来源与操作`} aria-expanded={expandedSource === key} disabled={locked} onClick={() => setExpandedSource(expandedSource === key ? null : key)}>来源</Button>
        </div>
      </div>
      {editing === key && <FieldEditor key={`${key}-${state.revision}`} name={key} field={field ?? seedField(key, defaults.get(key))} busy={locked} onSave={update} onCancel={() => setEditing(null)} />}
      {expandedSource === key && <div className="mt-3 text-xs text-[var(--ink-muted)]">
        {field?.source && <SourceDetails source={field.source} onSource={onSource} />}
        {!field?.source && isSystemDefault && <p>该值来自系统默认，可在上方修改覆盖。</p>}
        <div className="mt-1 flex flex-wrap gap-2">
          {field && rowStatus !== "removed" && <Button variant="ghost" size="sm" disabled={locked} onClick={() => void update({ op: "remove", field: key })}><X size={13} />撤销这项要求</Button>}
          {field?.previous && <Button variant="ghost" size="sm" disabled={locked} onClick={() => void update({ op: "restore", field: key })}><RotateCcw size={13} />恢复撤销前的要求</Button>}
        </div>
      </div>}
    </div>;
  };

  const groups: Array<"core" | "usage" | "reuse" | "prefs"> = ["core", "usage", "reuse", "prefs"];
  const reuseRows = rows.filter((row) => row.group === "reuse" && row.state !== "optionalUnknown" && row.state !== "removed");
  // 偏好组只在全是未指定时折叠;已有生效值、已撤销、待填写或冲突都不能藏进折叠区。
  const prefsCollapsed = !rows.some((row) => row.group === "prefs" && ["active", "removed", "conflict", "requiredUnknown"].includes(row.state));
  const unsupported = readiness?.unsupported_capabilities ?? [];
  const unresolved = (state.observations ?? []).filter((item) => !item.resolved);

  return <section aria-label="需求状态" tabIndex={-1} className="px-4 py-5 sm:px-6 [&_button]:min-h-11 [&_summary]:min-h-11 lg:[&_button]:min-h-8 lg:[&_summary]:min-h-0">
    <header>
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-base font-semibold">需求状态</h2>
        <div className="flex shrink-0 items-center gap-1">
          <span className="flex items-center gap-1 text-xs text-[var(--ink-muted)]">{headlineStatus(session) === "已确认" && <Check size={14} />}{headlineStatus(session)}</span>
          <Button variant="ghost" size="sm" onClick={onOpenEditor}>编辑全部</Button>
        </div>
      </div>
      <p className="mt-1 text-xs text-[var(--ink-muted)]">配置范围：主机（当前支持） · 已确定 {activeCount} 项{missing.length > 0 ? ` · 还缺 ${missing.length} 项` : ""}</p>
      <p className="mt-2 text-sm text-[var(--ink-muted)]">对话和这里的手动修改共用同一份需求；保存后立即生效，确认前不会生成配置。</p>
    </header>
    {missing.length > 0 && <p className="mt-4 flex items-start gap-2 text-sm text-[var(--ink-muted)]"><CircleHelp size={16} className="mt-0.5 shrink-0" /><span>还需要补充：{missing.map((key) => requirementLabel(key)).join("、")}。其余未指定项可按需填写。</span></p>}
    {state.changes.length > 0 && <div role="status" aria-live="polite" className="mt-4 border-l-2 border-l-[var(--primary)] pl-3">{state.changes.length > 3 ? <details><summary className="cursor-pointer text-sm">本轮更新 {state.changes.length} 项：{[...new Set(state.changes.map((change) => requirementLabel(change.field)))].join("、")}</summary><ul className="mt-2 space-y-1 text-sm text-[var(--ink-muted)]">{state.changes.map((change, index) => <li key={`${change.field}-${index}`}>{changeText(change)}</li>)}</ul></details> : <><p className="text-xs font-medium">本轮变化</p><ul className="mt-1 space-y-1 text-sm text-[var(--ink-muted)]">{state.changes.map((change, index) => <li key={`${change.field}-${index}`}>{changeText(change)}</li>)}</ul></>}</div>}

    <BudgetBlock session={session} locked={locked} onEdit={() => setEditing(editing === "budget_cny" ? null : "budget_cny")} editing={editing === "budget_cny"} onSource={onSource} editor={<FieldEditor key={`budget_cny-${state.revision}`} name="budget_cny" field={state.fields["budget_cny"] ?? seedField("budget_cny", undefined)} busy={locked} onSave={update} onCancel={() => setEditing(null)} />} />
    {editing === "budget_flex" && <FieldEditor key={`budget_flex-${state.revision}`} name="budget_flex" field={state.fields["budget_flex"] ?? seedField("budget_flex", defaults.get("budget_flex"))} busy={locked} onSave={update} onCancel={() => setEditing(null)} />}

    {groups.map((group) => {
      const groupRows = rows.filter((row) => row.group === group);
      if (group === "reuse") {
        if (reuseRows.length === 0) return null;
        return <details key={group} className="mt-4 border-t pt-4" open><summary className="cursor-pointer text-sm">{groupTitles[group]}</summary><div className="divide-y">{groupRows.filter((row) => reuseRows.some((item) => item.key === row.key)).map(renderRow)}</div></details>;
      }
      if (group === "prefs") {
        if (prefsCollapsed) {
          return <details key={group} className="mt-4 border-t pt-4"><summary className="cursor-pointer text-sm">{groupTitles[group]} · {groupRows.length} 项<span className="ml-2 text-xs text-[var(--ink-subtle)]">未指定的保持未指定，不作为选型限制</span></summary><div className="divide-y">{groupRows.map(renderRow)}</div></details>;
        }
        return <section key={group} className="mt-4 border-t pt-4"><h3 className="text-sm font-medium">{groupTitles[group]}</h3><div className="divide-y">{groupRows.map(renderRow)}</div></section>;
      }
      if (group === "core") {
        // 预算块已单独渲染;这里只补核心组其余字段。
        const rest = groupRows.filter((row) => row.key !== "budget_cny");
        if (rest.length === 0) return null;
        return <section key={group} className="mt-4 border-t pt-4"><h3 className="sr-only">{groupTitles[group]}</h3><div className="divide-y">{rest.map(renderRow)}</div></section>;
      }
      const visible = group === "usage" ? groupRows : groupRows.filter((row) => row.state !== "optionalUnknown");
      if (visible.length === 0) return null;
      return <section key={group} className="mt-4 border-t pt-4"><h3 className="text-sm font-medium">{groupTitles[group]}</h3><div className="divide-y">{visible.map(renderRow)}</div></section>;
    })}

    {unsupported.length > 0 && <section className="mt-4 border-t pt-4"><h3 className="text-sm font-medium">当前不支持</h3><ul className="mt-2 space-y-1 text-sm text-[var(--ink-muted)]">{unsupported.map((name) => <li key={name}>{capabilityLabels[name] ?? name}：当前版本不支持，未纳入配置</li>)}</ul></section>}
    {(unresolved.length > 0 || state.alternatives.length > 0) && <section className="mt-4 border-t pt-4"><h3 className="text-sm font-medium">未解决原话</h3><p className="mt-1 text-xs text-[var(--ink-muted)]">以下内容尚未明确，未纳入本次有效需求。</p><ul className="mt-2 space-y-3">{unresolved.map((item, index) => <li key={index} className="text-sm"><p className="whitespace-pre-wrap break-words">{item.text}</p><p className="mt-1 text-xs text-[var(--ink-muted)]">{item.reason}</p><SourceDetails source={item.source} onSource={onSource} />{!item.resolved && <Button variant="ghost" size="sm" disabled={locked} onClick={() => void update({ op: "remove", field: item.field || "notes" })}><X size={13} />撤销原话及{labels[item.field || "notes"] ?? "相关字段"}记录</Button>}</li>)}</ul>{state.alternatives.length > 0 && <ul className="mt-3 space-y-2">{state.alternatives.map((item, index) => <li key={`${item.field}-${index}`} className="text-sm text-[var(--ink-muted)]">存在歧义，本次未采用：{requirementLabel(item.field)}：{requirementValue(item.field, item.value)}</li>)}</ul>}</section>}
    <FreeRequirementForm busy={locked} onSave={update} />
    {state.history.length > 0 && <details className="mt-4 border-t pt-4"><summary className="cursor-pointer text-sm"><History size={14} className="mr-2 inline" />修改历史</summary><ol className="mt-3 space-y-4">{[...state.history].reverse().map((change, index) => <li key={`${change.revision}-${index}`} className="text-sm"><p>{changeText(change)}</p>{change.source && <SourceDetails source={change.source} onSource={onSource} />}</li>)}</ol></details>}
    {error && <p role="alert" className="mt-4 text-sm status-fail">{error} 当前输入已保留，请检查后重试。</p>}
    <PrimaryAction session={session} locked={locked || !!editing} onOpenReview={() => { setEditing(null); onOpenReview(); }} onOpenEditor={onOpenEditor} />
    <p className="mt-5 text-xs leading-5 text-[var(--ink-subtle)]">仅保存在当前装机会话中，刷新后可恢复；不会自动记为个人长期偏好。</p>
  </section>;
}

function seedField(key: string, value: unknown): Field {
  return { status: "unknown", ...(value !== undefined ? { value } : {}) } as Field;
}

/** 预算块:预算 / 弹性 / 上限都来自后端有效投影,前端不套 10% 规则。 */
function BudgetBlock({ session, locked, onEdit, editing, editor, onSource }: {
  session: Session;
  locked: boolean;
  onEdit: () => void;
  editing: boolean;
  editor: React.ReactNode;
  onSource: (messageID: string) => void;
}) {
  const [sourceOpen, setSourceOpen] = useState(false);
  const state = session.requirement_state;
  if (!state) return null;
  const spec = session.review_spec;
  const readiness = session.requirement_readiness;
  const flexDefault = (readiness?.effective_defaults ?? []).some((item) => item.field === "budget_flex");
  const budgetField = state.fields["budget_cny"];
  const required = (readiness?.missing_fields ?? []).includes("budget_cny");
  const budgetText = spec ? requirementValue("budget_cny", spec.budget_cny)
    : budgetField?.status === "active" ? requirementValue("budget_cny", budgetField.value)
    : required ? "待填写" : "未指定";
  const flexText = spec ? requirementValue("budget_flex", spec.budget_flex) : null;
  const ceiling = session.effective_budget_ceiling_cny;
  // must 才称“最高预算”;软偏好只能是“预算参考上沿”。
  const ceilingLabel = budgetField?.strength === "prefer" ? "预算参考上沿" : "最高预算";
  return <section className="mt-4 border-t pt-4" data-testid="budget-block">
    <div className="flex items-center justify-between gap-3">
      <h3 className="text-sm font-medium">核心需求</h3>
      <div className="flex shrink-0 items-center gap-1">
        {budgetField?.source && <Button variant="ghost" size="sm" aria-label="查看预算来源与操作" aria-expanded={sourceOpen} disabled={locked} onClick={() => setSourceOpen(!sourceOpen)}>来源</Button>}
        <Button variant="ghost" size="sm" aria-label="修改预算" disabled={locked} onClick={onEdit}><Pencil size={14} /><span>修改</span></Button>
      </div>
    </div>
    <dl className="mt-2 space-y-1 text-sm">
      <div className="flex gap-2"><dt className="text-[var(--ink-muted)]">预算</dt><dd className="tabular">{budgetText}</dd></div>
      {flexText && <div className="flex gap-2"><dt className="text-[var(--ink-muted)]">弹性</dt><dd className="tabular">{flexText}{flexDefault && <span className="ml-1 text-xs text-[var(--ink-subtle)]">（系统默认）</span>}</dd></div>}
      {ceiling != null && <div className="flex gap-2"><dt className="text-[var(--ink-muted)]">{ceilingLabel}</dt><dd className="tabular">¥{ceiling.toLocaleString("zh-CN")}</dd></div>}
    </dl>
    {editing && editor}
    {sourceOpen && budgetField?.source && <div className="mt-3 text-xs text-[var(--ink-muted)]">
      <p className="whitespace-pre-wrap break-words leading-5">{budgetField.source.kind === "edit" ? "手动修改" : "来自对话"}：{budgetField.source.quote || "已保存的需求操作"}</p>
      {budgetField.source.message_id && <Button variant="ghost" size="sm" className="mt-1" onClick={() => onSource(budgetField.source!.message_id!)}>查看来源消息<ArrowUpRight size={13} /></Button>}
    </div>}
  </section>;
}

/** 需求 Tab 底部唯一主要操作;状态全部来自服务端三轴与 readiness。 */
function PrimaryAction({ session, locked, onOpenReview, onOpenEditor }: { session: Session; locked: boolean; onOpenReview: () => void; onOpenEditor: () => void }) {
  const readiness = session.requirement_readiness;
  const confirmation = session.requirement_confirmation;
  const relation = session.build_relation;
  const missing = readiness?.missing_fields ?? [];
  if (relation.status === "running") {
    return <div className="mt-6 border-t pt-4 text-sm text-[var(--ink-muted)]" data-testid="requirement-primary">
      <p>正在生成并校验配置，本次生成基于已确认的需求快照{confirmation.confirmed_revision != null ? `（修订 ${confirmation.confirmed_revision}）` : ""}。期间可以继续修改需求，确认与生成暂不可用。</p>
      <Button variant="outline" className="mt-3" onClick={onOpenEditor}>编辑全部需求</Button>
    </div>;
  }
  if (!readiness?.confirmation_eligible) {
    return <div className="mt-6 border-t pt-4" data-testid="requirement-primary">
      <Button className="w-full" disabled aria-describedby="requirement-missing-reason">核对当前需求</Button>
      <p id="requirement-missing-reason" className="mt-2 text-xs text-[var(--ink-muted)]">{missing.length > 0 ? `还缺：${missing.slice(0, 2).map((key) => requirementLabel(key)).join("、")}${missing.length > 2 ? ` 等 ${missing.length} 项` : ""}` : "存在待确认的要求，请先处理。"}</p>
    </div>;
  }
  const label = relation.status === "failed" && confirmation.status === "confirmed" ? "按相同需求重新生成"
    : confirmation.status === "modified" ? "重新核定并生成"
    : confirmation.status === "confirmed" ? "重新生成配置"
    : "核对当前需求";
  return <div className="mt-6 border-t pt-4" data-testid="requirement-primary">
    {confirmation.status === "confirmed" && relation.status === "current"
      ? <p className="flex items-center gap-1 text-sm"><Check size={15} className="status-pass" />需求已确认，当前配置基于这份需求。</p>
      : <Button className="w-full" disabled={locked} onClick={onOpenReview}>{locked && <Loader2 className="animate-spin" size={15} />}{label}</Button>}
  </div>;
}

function SourceDetails({ source, onSource }: { source: Source; onSource: (id: string) => void }) {
  return <div className="text-xs text-[var(--ink-muted)]"><p className="whitespace-pre-wrap break-words leading-5">{sourceLabel(source) ?? "需求操作"}：{source.quote || "已保存的需求操作"}</p>{source.message_id && <Button variant="ghost" size="sm" className="mt-1" onClick={() => onSource(source.message_id)}>查看来源消息<ArrowUpRight size={13} /></Button>}</div>;
}

function changeText(change: RequirementState["changes"][number]) {
  const label = requirementLabel(change.field);
  if (change.op === "remove") return `已撤销${label}`;
  if (change.op === "alternative") return `${label}：记录为备选，当前要求不变`;
  if (change.op === "conflict") return `${label}：有冲突，需要澄清`;
  const after = requirementValue(change.field, change.after?.value);
  if (change.op === "restore") return `${label}：恢复为${after}`;
  const before = change.before?.status === "active" ? requirementValue(change.field, change.before.value) : null;
  return `${label}：${before ? `${before} → ` : ""}${after}`;
}

function FieldEditor({ name, field, busy, onSave, onCancel }: { name: string; field: Field; busy: boolean; onSave: (value: RequirementOperation) => Promise<boolean>; onCancel: () => void }) {
  const currentValue = field.status === "active" || field.status === "conflict" ? field.value : undefined;
  const [value, setValue] = useState<unknown>(currentValue ?? "");
  const [strength, setStrength] = useState<"must" | "prefer">(field.strength ?? "must");
  const [scope, setScope] = useState<"session" | "temporary">(field.scope ?? "session");
  // Editing a fixed field explicitly corrects a legacy background misbinding.
  const initialKind = field.kind === "context" && name !== "notes" && name !== "recipient" && !name.startsWith("free.")
    ? (name.startsWith("use_case.") || ["recipient", "owned_parts", "existing_parts"].includes(name) ? "fact" : "constraint")
    : field.kind;
  const [kind, setKind] = useState<NonNullable<Field["kind"]> | "">(initialKind ?? "");
  const [error, setError] = useState("");
  const label = requirementLabel(name);
  const save = async () => {
    if (name === "notes" && !kind) { setError("请选择这段说明的信息用途，以便正确用于配置。"); return; }
    if (value === "" || value === undefined) { setError("请填写需求值；如需取消这项要求，请使用撤销。"); return; }
    let parsed: unknown = value;
    if (["budget_cny", "budget_flex", "use_case.fps_target"].includes(name)) parsed = Number(value);
    if (["use_case.titles"].includes(name)) parsed = String(value).split(/[，,、\n]/).map((item) => item.trim()).filter(Boolean);
    if (parsed === "" || parsed === undefined || (typeof parsed === "number" && (!Number.isFinite(parsed) || parsed < 0))) { setError("请填写有效的需求值；如需取消这项要求，请使用撤销。"); return; }
    setError("");
    await onSave({ op: "set", field: name, value: parsed, strength, scope, ...(kind ? { kind } : {}) });
  };
  return <div className="mt-3 space-y-3 border-l-2 border-l-[var(--hairline-strong)] pl-3">
    {name === "owned_parts" ? <OwnedPartsEditor value={value} onChange={setValue} disabled={busy} /> : name === "existing_parts" || name === "priority" ? <fieldset><legend className="mb-2 text-xs text-[var(--ink-muted)]">{label}</legend><div className="grid grid-cols-2 gap-2">{categories.map((category) => <label key={category} className="flex min-h-11 items-center gap-2 text-sm"><input type="checkbox" disabled={busy} checked={Array.isArray(value) && value.includes(category)} onChange={(event) => setValue(event.target.checked ? [...(Array.isArray(value) ? value : []), category] : (Array.isArray(value) ? value : []).filter((item) => item !== category))} />{categoryLabels[category]}</label>)}</div></fieldset> : <label className="block text-xs text-[var(--ink-muted)]">{label}{name === "budget_flex" ? "（小数，如 0.1 表示 10%）" : ""}{choices[name] ? <select aria-label={`编辑${label}`} className={`${selectClass} mt-1`} value={String(value)} disabled={busy} onChange={(event) => setValue(event.target.value)}><option value="" disabled>请选择</option>{choices[name].map(([key, text]) => <option key={key} value={key}>{text}</option>)}</select> : name === "notes" ? <Textarea aria-label={`编辑${label}`} className="mt-1" value={String(value)} disabled={busy} onChange={(event) => setValue(event.target.value)} rows={3} /> : <Input aria-label={`编辑${label}`} className="mt-1" autoFocus value={Array.isArray(value) ? value.join("、") : String(value)} type={["budget_cny", "budget_flex", "use_case.fps_target"].includes(name) ? "number" : "text"} step={name === "budget_flex" ? 0.01 : 1} disabled={busy} onChange={(event) => setValue(event.target.value)} />}</label>}
    {name === "notes" && <label className="block text-xs text-[var(--ink-muted)]">信息用途<select aria-label={`${label}信息用途`} className={`${selectClass} mt-1`} value={kind} disabled={busy} onChange={(event) => setKind(event.target.value as NonNullable<Field["kind"]>)}><option value="" disabled>请选择信息用途</option>{Object.entries(kindLabels).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select><span className="mt-1 block leading-5">用途事实与补充说明用于理解场景；配置条件会按必须或尽量满足执行。</span></label>}
    <div className="grid grid-cols-2 gap-3">{kind !== "fact" && kind !== "context" && <label className="text-xs text-[var(--ink-muted)]">要求强度<select aria-label={`${label}要求强度`} className={`${selectClass} mt-1`} value={strength} disabled={busy} onChange={(event) => setStrength(event.target.value as "must" | "prefer")}><option value="must">必须满足</option><option value="prefer">尽量满足</option></select></label>}<label className="text-xs text-[var(--ink-muted)]">适用范围<select aria-label={`${label}适用范围`} className={`${selectClass} mt-1`} value={scope} disabled={busy} onChange={(event) => setScope(event.target.value as "session" | "temporary")}><option value="session">本次会话</option><option value="temporary">临时例外</option></select></label></div>
    {error && <p role="alert" className="text-xs status-fail">{error}</p>}<div className="flex justify-end gap-2"><Button variant="ghost" size="sm" disabled={busy} onClick={onCancel}>取消</Button><Button variant="outline" size="sm" disabled={busy} onClick={() => void save()}>{busy && <Loader2 size={14} className="animate-spin" />}保存需求</Button></div>
  </div>;
}

function OwnedPartsEditor({ value, onChange, disabled }: { value: unknown; onChange: (value: unknown) => void; disabled: boolean }) {
  const parts = Array.isArray(value) ? value as { category: string; model: string; quantity: number }[] : [];
  const change = (index: number, patch: Partial<(typeof parts)[number]>) => onChange(parts.map((part, current) => current === index ? { ...part, ...patch } : part));
  return <fieldset className="space-y-3"><legend className="mb-2 text-xs text-[var(--ink-muted)]">已有配件型号</legend>{parts.map((part, index) => <div key={index} className="space-y-2"><div className="flex gap-2"><select aria-label={`已有配件 ${index + 1} 类别`} className={selectClass} value={part.category} disabled={disabled} onChange={(event) => change(index, { category: event.target.value })}>{categories.map((category) => <option key={category} value={category}>{categoryLabels[category]}</option>)}</select><Button variant="ghost" size="sm" disabled={disabled} onClick={() => onChange(parts.filter((_, current) => current !== index))}>移除</Button></div><Input aria-label={`已有配件 ${index + 1} 完整型号`} placeholder="完整型号" value={part.model} disabled={disabled} onChange={(event) => change(index, { model: event.target.value })} /><label className="block text-xs text-[var(--ink-muted)]">数量<Input aria-label={`已有配件 ${index + 1} 数量`} className="mt-1" type="number" min={1} value={part.quantity} disabled={disabled} onChange={(event) => change(index, { quantity: Number(event.target.value) })} /></label></div>)}<Button variant="outline" size="sm" disabled={disabled} onClick={() => onChange([...parts, { category: "cpu", model: "", quantity: 1 }])}>添加已有配件</Button></fieldset>;
}

function FreeRequirementForm({ busy, onSave }: { busy: boolean; onSave: (op: RequirementOperation) => Promise<boolean> }) {
  const [text, setText] = useState("");
  const [open, setOpen] = useState(false);
  const [strength, setStrength] = useState<"must" | "prefer">("prefer");
  return <details className="mt-4 border-t pt-4" onToggle={e => setOpen(e.currentTarget.open)}>
    <summary className="cursor-pointer text-sm">添加其他要求</summary>
    {open && <>
      <label className="mt-3 block text-sm">具体要求<Textarea className="mt-2" value={text} onChange={e => setText(e.target.value)} disabled={busy} placeholder="例如：桌面空间有限，机箱尽量放在显示器后面" /></label>
      <label className="mt-3 block text-sm">重要程度<select aria-label="重要程度" className={selectClass} value={strength} onChange={e => setStrength(e.target.value as "must" | "prefer")}><option value="prefer">尽量满足</option><option value="must">必须满足</option></select></label>
      <Button className="mt-3" variant="outline" disabled={busy || !text.trim()} onClick={() => void onSave({ op: "set", field: "free." + crypto.randomUUID(), value: text.trim(), kind: "constraint", strength, evidence: "stated" }).then(saved => { if (saved) setText(""); })}>保存要求</Button>
    </>}
  </details>;
}
