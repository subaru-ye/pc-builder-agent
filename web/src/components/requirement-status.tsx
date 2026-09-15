"use client";

import { useState } from "react";
import { ArrowUpRight, Check, CircleHelp, History, Loader2, Pencil, RotateCcw, X } from "lucide-react";
import type { RequirementOperation, RequirementState, Session } from "@/lib/api/types";
import { categories, categoryLabels } from "@/lib/domain";
import { userMessage } from "@/lib/api/problem";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Textarea } from "./ui/textarea";

type Field = RequirementState["fields"][string];
type Source = NonNullable<Field["source"]>;

const labels: Record<string, string> = {
  budget_cny: "预算", budget_basis: "预算口径", budget_flex: "预算弹性",
  "use_case.type": "主要用途", "use_case.titles": "游戏 / 应用", "use_case.resolution": "分辨率", "use_case.fps_target": "目标帧率",
  existing_parts: "已有配件类别", owned_parts: "已有配件型号", "brand_pref.cpu": "CPU 品牌", "brand_pref.gpu": "显卡品牌",
  noise_pref: "静音", appearance: "外观", size_pref: "尺寸", notes: "补充说明", recipient: "装机对象", priority: "优先投入",
};
const orderedFields = Object.keys(labels);
export function requirementLabel(key: string) { return labels[key] ?? (key.startsWith("free.") ? "补充要求" : key); }
const kindLabels = { fact: "用途事实", context: "补充说明", constraint: "配置条件" };

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
  "brand_pref.cpu": [["any", "不限"], ["amd", "AMD"], ["intel", "Intel"]],
  "brand_pref.gpu": [["any", "不限"], ["amd", "AMD"], ["nvidia", "NVIDIA"]],
  noise_pref: [["any", "不限"], ["silent", "安静"], ["normal", "常规"]],
  size_pref: [["any", "不限"], ["atx", "ATX"], ["matx", "M-ATX"], ["itx", "ITX"]],
};
const selectClass = "h-11 w-full rounded-md border bg-[var(--canvas)] px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--primary)]";

export function requirementValue(field: string, value: unknown): string {
  if (value === undefined || value === null) return "未知";
  if (field === "budget_cny") return `¥${Number(value).toLocaleString("zh-CN")}`;
  if (field === "budget_flex") return Number(value) === 0 ? "严格预算" : `${Math.round(Number(value) * 100)}%`;
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

function stateLabel(session: Session) {
  return { collecting: "收集中", ready_to_confirm: "待确认", confirmed: "已确认", modified: "确认后有修改" }[session.requirement_status] ?? "收集中";
}

export function RequirementSummary({ session, onOpen }: { session: Session; onOpen: () => void }) {
  const state = session.requirement_state;
  if (!state) return null;
  const summary = ["budget_cny", "use_case.type", "use_case.resolution"].map((key) => state.fields[key]?.status === "active" ? requirementValue(key, state.fields[key].value) : `${labels[key]}未知`).join(" · ");
  const changedFields = [...new Set(state.changes.map((change) => requirementLabel(change.field)))];
  return <div aria-label="需求摘要" className="px-4 py-2 sm:px-6"><div className="flex items-center justify-between gap-3"><div className="min-w-0"><div className="flex flex-wrap items-baseline gap-x-3 gap-y-1"><p className="text-xs text-[var(--ink-muted)]">当前需求 · {stateLabel(session)}</p><p className="text-sm">{summary}</p></div>{changedFields.length > 0 && <p className="mt-1 truncate text-xs text-[var(--ink-muted)]" title={changedFields.join("、")}>本轮更新：{changedFields.slice(0, 3).join("、")}{changedFields.length > 3 ? ` 等 ${changedFields.length} 项` : ""}</p>}</div><Button variant="ghost" size="sm" className="min-h-11 shrink-0" aria-label="查看 / 修改" onClick={onOpen}><span className="sm:hidden">查看</span><span className="hidden sm:inline">查看 / 修改</span><ArrowUpRight size={14} /></Button></div></div>;
}

export function RequirementStatus({ session, busy, onUpdate, onConfirm, onSource }: {
  session: Session;
  busy: boolean;
  onUpdate: (operations: RequirementOperation[]) => Promise<void>;
  onConfirm: () => Promise<void>;
  onSource: (messageID: string) => void;
}) {
  const [editing, setEditing] = useState<string | null>(null);
  const [expandedSource, setExpandedSource] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const state = session.requirement_state;
  if (!state) return null;
  const keys = [...orderedFields.filter((key) => key in state.fields), ...Object.keys(state.fields).filter((key) => !(key in labels))];
  const active = keys.filter((key) => ["active", "conflict"].includes(state.fields[key].status));
  const unknown = keys.filter((key) => !["active", "conflict"].includes(state.fields[key].status));
  const requiredUnknown = unknown.filter((key) => session.missing_fields?.includes(key));
  const optionalUnknown = unknown.filter((key) => !requiredUnknown.includes(key));
  const locked = busy || saving;
  const update = async (operation: RequirementOperation) => {
    setSaving(true); setError(null);
    try { await onUpdate([operation]); setEditing(null); return true; }
    catch (cause) { setError(userMessage(cause)); return false; }
    finally { setSaving(false); }
  };
  const confirm = async () => {
    setSaving(true); setError(null);
    try { await onConfirm(); }
    catch (cause) { setError(userMessage(cause)); }
    finally { setSaving(false); }
  };

  return <section aria-label="当前需求" className="px-4 py-5 sm:px-6 [&_button]:min-h-11 [&_summary]:min-h-11 lg:[&_button]:min-h-8 lg:[&_summary]:min-h-0">
    <header><div className="flex items-center justify-between gap-3"><h2 className="text-base font-semibold">当前需求</h2><span className="flex items-center gap-1 text-xs text-[var(--ink-muted)]">{stateLabel(session) === "已确认" && <Check size={14} />}{stateLabel(session)}</span></div><p className="mt-2 text-sm text-[var(--ink-muted)]">这里是本次对话已记录的需求，补充或修改后会用于后续追问和配置。</p></header>
    {session.requirement_status === "modified" && <p className="mt-4 border-l-2 border-l-[var(--review)] pl-3 text-sm text-[var(--ink-muted)]">以下为修改中的草稿。已确认需求和已有配置保持原样，重新确认后生成新版本。</p>}
    {(session.missing_fields?.length ?? 0) > 0 && <p className="mt-4 flex items-start gap-2 text-sm text-[var(--ink-muted)]"><CircleHelp size={16} className="mt-0.5 shrink-0" /><span>还需要补充：{session.missing_fields.map((key) => requirementLabel(key)).join("、")}。其余未知项可按需填写。</span></p>}
    {state.changes.length > 0 && <div role="status" aria-live="polite" className="mt-4 border-l-2 border-l-[var(--primary)] pl-3">{state.changes.length > 3 ? <details><summary className="cursor-pointer text-sm">本轮更新 {state.changes.length} 项：{[...new Set(state.changes.map((change) => requirementLabel(change.field)))].join("、")}</summary><ul className="mt-2 space-y-1 text-sm text-[var(--ink-muted)]">{state.changes.map((change, index) => <li key={`${change.field}-${index}`}>{changeText(change)}</li>)}</ul></details> : <><p className="text-xs font-medium">本轮变化</p><ul className="mt-1 space-y-1 text-sm text-[var(--ink-muted)]">{state.changes.map((change, index) => <li key={`${change.field}-${index}`}>{changeText(change)}</li>)}</ul></>}</div>}
    {active.length === 0 && <p className="my-6 flex items-start gap-2 text-sm text-[var(--ink-muted)]"><CircleHelp size={16} className="mt-0.5 shrink-0" />还没有记录到明确需求。可以继续对话，也可以在下方补充。</p>}
    <div className="mt-4 divide-y">{[...active, ...requiredUnknown].map((key) => {
      const field = state.fields[key];
      const isUnknown = requiredUnknown.includes(key);
      const meaning = fieldMeaning(key, field);
      return <div key={key} className="py-3"><div className="flex items-center gap-3"><div className="min-w-0 flex-1"><div className="flex flex-wrap items-center gap-x-2 gap-y-1"><span className="text-xs text-[var(--ink-muted)]">{requirementLabel(key)}</span>{!isUnknown && meaning && <span className="text-xs text-[var(--ink-subtle)]">{meaning}</span>}{field.scope === "temporary" && <span className="text-xs status-review">临时例外</span>}{field.status === "conflict" && <span className="text-xs status-review">待澄清</span>}</div><p className={`mt-1 break-words whitespace-pre-wrap text-sm ${isUnknown ? "text-[var(--ink-subtle)]" : ""}`}>{isUnknown ? "未知 · 需要补充" : requirementValue(key, field.value)}</p></div><div className="flex shrink-0 items-center gap-1"><Button variant="ghost" size="sm" aria-label={`${isUnknown ? "补充" : "修改"}${requirementLabel(key)}`} disabled={locked} onClick={() => setEditing(editing === key ? null : key)}><Pencil size={14} /><span>{isUnknown ? "补充" : "修改"}</span></Button>{!isUnknown && <Button variant="ghost" size="sm" aria-label={`查看${requirementLabel(key)}来源与操作`} aria-expanded={expandedSource === key} onClick={() => setExpandedSource(expandedSource === key ? null : key)}>来源</Button>}</div></div>
        {editing === key && <FieldEditor key={`${key}-${state.revision}`} name={key} field={field} busy={locked} onSave={update} onCancel={() => setEditing(null)} />}
        {expandedSource === key && <div className="mt-3 text-xs text-[var(--ink-muted)]">{field.source && <SourceDetails source={field.source} onSource={onSource} />}<div className="mt-1 flex flex-wrap gap-2"><Button variant="ghost" size="sm" disabled={locked} onClick={() => void update({ op: "remove", field: key })}><X size={13} />撤销这项要求</Button>{field.previous && <Button variant="ghost" size="sm" disabled={locked} onClick={() => void update({ op: "restore", field: key })}><RotateCcw size={13} />恢复例外前要求</Button>}</div></div>}
      </div>;
    })}</div>
    {optionalUnknown.length > 0 && <details className="mt-4 border-t pt-4"><summary className="cursor-pointer text-sm">尚未说明 · {optionalUnknown.length} 项<span className="ml-2 text-xs text-[var(--ink-subtle)]">均为未知</span></summary><p className="mt-2 text-xs leading-6 text-[var(--ink-muted)]">{optionalUnknown.map((key) => `${requirementLabel(key)}：未知${state.fields[key].status === "removed" ? "（已撤销）" : ""}`).join(" · ")}</p><div className="mt-2 flex flex-wrap gap-1">{optionalUnknown.map((key) => <Button key={key} variant="ghost" size="sm" disabled={locked} onClick={() => setEditing(editing === key ? null : key)}>补充{requirementLabel(key)}</Button>)}</div>{editing && optionalUnknown.includes(editing) && <FieldEditor key={`${editing}-${state.revision}`} name={editing} field={state.fields[editing]} busy={locked} onSave={update} onCancel={() => setEditing(null)} />}</details>}
    {state.alternatives.length > 0 && <details className="mt-4 border-t pt-4"><summary className="cursor-pointer text-sm">讨论中的备选 · {state.alternatives.length} 项</summary><p className="mt-2 text-xs text-[var(--ink-muted)]">尚未作为当前要求生效。</p><ul className="mt-2 space-y-3">{state.alternatives.map((item, index) => <li key={`${item.field}-${index}`} className="text-sm"><p>{requirementLabel(item.field)}：{requirementValue(item.field, item.value)}</p>{item.source && <SourceDetails source={item.source} onSource={onSource} />}</li>)}</ul></details>}
    {!!state.observations?.length && <details className="mt-4 border-t pt-4"><summary className="cursor-pointer text-sm">保留的原话 · {state.observations.filter((item) => !item.resolved).length} 项待明确</summary><p className="mt-2 text-xs text-[var(--ink-muted)]">尚未明确的内容仍会用于后续理解，已确定的需求可以继续保存。</p><ul className="mt-3 space-y-4">{state.observations.map((item, index) => <li key={index} className="text-sm"><p className="whitespace-pre-wrap break-words">{item.text}</p><p className="mt-1 text-xs text-[var(--ink-muted)]">{item.resolved ? "已澄清，不再作为待确认内容" : item.reason}</p><SourceDetails source={item.source} onSource={onSource} />{!item.resolved && <Button variant="ghost" size="sm" disabled={locked} onClick={() => void update({ op: "remove", field: item.field || "notes" })}><X size={13} />撤销原话及{labels[item.field || "notes"] ?? "相关字段"}记录</Button>}</li>)}</ul></details>}
    <FreeRequirementForm busy={locked} onSave={update} />
    {state.history.length > 0 && <details className="mt-4 border-t pt-4"><summary className="cursor-pointer text-sm"><History size={14} className="mr-2 inline" />修改历史</summary><ol className="mt-3 space-y-4">{[...state.history].reverse().map((change, index) => <li key={`${change.revision}-${index}`} className="text-sm"><p>{changeText(change)}</p>{change.source && <SourceDetails source={change.source} onSource={onSource} />}</li>)}</ol></details>}
    {error && <p role="alert" className="mt-4 text-sm status-fail">{error} 当前输入已保留，请检查后重试。</p>}
    {session.phase === "requirement_ready" && session.pending_requirement && <div className="mt-6 border-t pt-4"><p className="mb-3 text-sm text-[var(--ink-muted)]">确认本次有效需求后开始选配；未知项不会被默认值替代。</p><Button className="w-full" disabled={locked || !!editing} onClick={() => void confirm()}>{locked && <Loader2 className="animate-spin" size={15} />}{session.confirmed_requirement_state ? "确认修改并继续选配" : "确认并开始选配"}</Button>{editing && <p className="mt-2 text-xs text-[var(--ink-muted)]">请先保存或取消正在编辑的需求。</p>}</div>}
    <p className="mt-5 text-xs leading-5 text-[var(--ink-subtle)]">仅保存在当前装机会话中，刷新后可恢复；不会自动记为个人长期偏好。</p>
  </section>;
}

function SourceDetails({ source, onSource }: { source: Source; onSource: (id: string) => void }) {
  return <div className="text-xs text-[var(--ink-muted)]"><p className="whitespace-pre-wrap break-words leading-5">{source.kind === "edit" ? "界面修改" : source.kind === "confirmed" ? "历史确认需求" : "用户消息"}：{source.quote || "已保存的需求操作"}</p>{source.message_id && <Button variant="ghost" size="sm" className="mt-1" onClick={() => onSource(source.message_id)}>查看来源消息<ArrowUpRight size={13} /></Button>}</div>;
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
  const initialKind = field.kind === "context" && name !== "notes" && !name.startsWith("free.")
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
