"use client";

// 完整需求抽屉(最大宽 672px):复杂字段与批量编辑。复用与侧栏相同的
// PATCH requirement-state operations 合同;未保存值只存在表单状态,
// 保存成功以服务端返回的完整 Session 更新缓存,409 保留输入。
// diff 基线是表单打开时的有效值(active 或系统默认预填):只有用户实际
// 改动的字段才产生操作,系统默认不会被误写成 active 用户值。
import { zodResolver } from "@hookform/resolvers/zod";
import { Controller, useForm } from "react-hook-form";
import { useState } from "react";
import { Loader2, Save } from "lucide-react";
import type { RequirementOperation, RequirementState, Session } from "@/lib/api/types";
import { categoryLabels } from "@/lib/domain";
import { userMessage } from "@/lib/api/problem";
import { requirementValue } from "./requirement-status";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Textarea } from "./ui/textarea";
import { z } from "zod";

const selectClass = "h-11 w-full rounded-md border bg-[var(--canvas)] px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--primary)]";

// 数字输入留空表示未指定;非空时与后端 schemas 同口径校验:
// 预算/帧率为正整数,预算弹性为 [0, 0.3] 的小数。
const positiveInt = z.string().refine((raw) => raw.trim() === "" || (/^\d+$/).test(raw.trim()) && Number(raw) > 0, "请填写正整数");
const flexRatio = z.string().refine((raw) => {
  if (raw.trim() === "") return true;
  const value = Number(raw);
  return Number.isFinite(value) && value >= 0 && value <= 0.3;
}, "预算弹性须为 0–0.3 的小数（如 0.1 表示 10%）");

const editorSchema = z.object({
  budget_cny: positiveInt,
  budget_flex: flexRatio,
  budget_basis: z.string(),
  use_case_type: z.string(),
  use_case_titles: z.string(),
  use_case_resolution: z.string(),
  use_case_fps_target: positiveInt,
  size_pref: z.string(),
  noise_pref: z.string(),
  brand_pref_cpu: z.string(),
  brand_pref_gpu: z.string(),
  existing_parts: z.array(z.string()),
  owned_parts: z.array(z.object({ category: z.string(), model: z.string().refine((model) => model.trim() !== "", "请填写完整型号"), quantity: z.number().int().min(1) })),
  priority: z.array(z.string()),
  notes: z.string(),
  recipient: z.string(),
  appearance: z.string(),
});

type EditorValue = z.input<typeof editorSchema>;

function activeValue(state: RequirementState, key: string): unknown {
  const field = state.fields[key];
  return field && (field.status === "active" || field.status === "conflict") ? field.value : undefined;
}

function initialValues(state: RequirementState, defaults: Map<string, unknown>): EditorValue {
  const value = (key: string) => {
    const current = activeValue(state, key);
    if (current !== undefined) return current;
    return defaults.get(key);
  };
  const owned = value("owned_parts");
  const str = (key: string) => (value(key) === undefined || value(key) === null ? "" : String(value(key)));
  return {
    budget_cny: str("budget_cny"),
    budget_flex: str("budget_flex"),
    budget_basis: str("budget_basis"),
    use_case_type: str("use_case.type"),
    use_case_titles: Array.isArray(value("use_case.titles")) ? (value("use_case.titles") as string[]).join("，") : "",
    use_case_resolution: str("use_case.resolution"),
    use_case_fps_target: str("use_case.fps_target"),
    size_pref: str("size_pref"),
    noise_pref: str("noise_pref"),
    brand_pref_cpu: str("brand_pref.cpu") || "any",
    brand_pref_gpu: str("brand_pref.gpu") || "any",
    existing_parts: Array.isArray(value("existing_parts")) ? value("existing_parts") as string[] : [],
    owned_parts: Array.isArray(owned) ? owned as EditorValue["owned_parts"] : [],
    priority: Array.isArray(value("priority")) ? value("priority") as string[] : [],
    notes: str("notes"),
    recipient: str("recipient"),
    appearance: str("appearance"),
  };
}

// 与侧栏行内编辑保持同一语义:有既有值才比较,否则视为未表达。
// 只比较 next 与基线(用户实际改动),再决定 set/remove:
// 清空一个预填的系统默认不产生操作(回到未表达,默认继续生效);
// 清空一个 active 用户值才产生 remove。
function diffOperations(state: RequirementState, baseline: EditorValue, next: EditorValue): RequirementOperation[] {
  const operations: RequirementOperation[] = [];
  const current = (key: string) => activeValue(state, key);
  const changed = (a: unknown, b: unknown) => JSON.stringify(a) !== JSON.stringify(b);
  const set = (key: string, value: unknown, strength: "must" | "prefer", kind?: "fact" | "context" | "constraint") => {
    operations.push({ op: "set", field: key, value, strength, ...(kind ? { kind } : {}), ...(key === "notes" || key === "recipient" || key === "appearance" ? { kind: kind ?? "context" } : {}) });
  };
  const remove = (key: string) => { if (current(key) !== undefined) operations.push({ op: "remove", field: key }); };
  // raw 相对基线未变 → 用户没有表达;清空 → 仅撤销既有 active 值;否则提交新值。
  const clearOrSet = (key: string, rawNext: string, rawBaseline: string, value: () => unknown, strength: "must" | "prefer", kind?: "fact" | "context" | "constraint") => {
    if (!changed(rawNext, rawBaseline)) return;
    if (rawNext.trim() === "") remove(key);
    else set(key, value(), strength, kind);
  };
  clearOrSet("budget_cny", next.budget_cny, baseline.budget_cny, () => Number(next.budget_cny), "must");
  clearOrSet("budget_flex", next.budget_flex, baseline.budget_flex, () => Number(next.budget_flex), "prefer", "constraint");
  clearOrSet("budget_basis", next.budget_basis, baseline.budget_basis, () => next.budget_basis, "must", "constraint");
  clearOrSet("use_case.type", next.use_case_type, baseline.use_case_type, () => next.use_case_type, "must", "constraint");
  const titles = (raw: string) => (raw.trim() === "" ? "" : JSON.stringify(raw.split(/[，,、\n]/).map((item) => item.trim()).filter(Boolean)));
  if (changed(titles(next.use_case_titles), titles(baseline.use_case_titles))) {
    if (next.use_case_titles.trim() === "") remove("use_case.titles");
    else set("use_case.titles", next.use_case_titles.split(/[，,、\n]/).map((item) => item.trim()).filter(Boolean), "must", "fact");
  }
  clearOrSet("use_case.resolution", next.use_case_resolution, baseline.use_case_resolution, () => next.use_case_resolution, "must", "constraint");
  clearOrSet("use_case.fps_target", next.use_case_fps_target, baseline.use_case_fps_target, () => Number(next.use_case_fps_target), "prefer", "fact");
  clearOrSet("size_pref", next.size_pref, baseline.size_pref, () => next.size_pref, "prefer", "constraint");
  clearOrSet("noise_pref", next.noise_pref, baseline.noise_pref, () => next.noise_pref, "prefer", "constraint");
  clearOrSet("brand_pref.cpu", next.brand_pref_cpu, baseline.brand_pref_cpu, () => next.brand_pref_cpu, "prefer", "constraint");
  clearOrSet("brand_pref.gpu", next.brand_pref_gpu, baseline.brand_pref_gpu, () => next.brand_pref_gpu, "prefer", "constraint");
  const arrayOrClear = (key: string, nextItems: unknown, baselineItems: unknown, value: () => unknown, strength: "must" | "prefer", kind?: "fact" | "context" | "constraint") => {
    if (!changed(nextItems, baselineItems)) return;
    if (Array.isArray(nextItems) && nextItems.length === 0) remove(key);
    else set(key, value(), strength, kind);
  };
  arrayOrClear("existing_parts", next.existing_parts, baseline.existing_parts, () => [...next.existing_parts], "must", "fact");
  arrayOrClear("owned_parts", next.owned_parts, baseline.owned_parts, () => next.owned_parts.map((part) => ({ category: part.category, model: part.model.trim(), quantity: part.quantity })), "must", "fact");
  arrayOrClear("priority", next.priority, baseline.priority, () => [...next.priority], "prefer", "constraint");
  clearOrSet("notes", next.notes.trim(), baseline.notes.trim(), () => next.notes.trim(), "prefer", "context");
  clearOrSet("recipient", next.recipient.trim(), baseline.recipient.trim(), () => next.recipient.trim(), "prefer", "context");
  clearOrSet("appearance", next.appearance.trim(), baseline.appearance.trim(), () => next.appearance.trim(), "prefer", "context");
  return operations;
}

const editorFields: Array<{ name: keyof EditorValue; label: string; type: "text" | "number" | "select" | "textarea"; options?: [string, string][]; hint?: string }> = [
  { name: "budget_cny", label: "预算（元）", type: "number" },
  { name: "budget_flex", label: "预算弹性（小数，0.1 表示 10%；留空保持未指定）", type: "number", hint: "留空则继续使用系统默认" },
  { name: "budget_basis", label: "预算口径", type: "select", options: [["", "未指定"], ["new_purchase", "仅用于新增购买"], ["full_build", "整机参考总价（包含已有件）"]] },
  { name: "use_case_type", label: "主要用途", type: "select", options: [["", "未指定"], ["gaming", "游戏"], ["productivity", "生产力"], ["general", "日常综合"]] },
  { name: "use_case_titles", label: "游戏 / 应用（逗号分隔）", type: "text" },
  { name: "use_case_resolution", label: "分辨率", type: "select", options: [["", "未指定"], ["1080p", "1080p"], ["2K", "2K"], ["4K", "4K"]] },
  { name: "use_case_fps_target", label: "目标帧率", type: "number" },
  { name: "size_pref", label: "尺寸", type: "select", options: [["", "未指定"], ["any", "不限"], ["atx", "ATX"], ["matx", "M-ATX"], ["itx", "ITX"]] },
  { name: "noise_pref", label: "静音", type: "select", options: [["", "未指定"], ["any", "不限"], ["silent", "安静"], ["normal", "常规"]] },
  { name: "brand_pref_cpu", label: "CPU 品牌", type: "select", options: [["any", "不限"], ["amd", "AMD"], ["intel", "Intel"]] },
  { name: "brand_pref_gpu", label: "显卡品牌", type: "select", options: [["any", "不限"], ["amd", "AMD"], ["nvidia", "NVIDIA"]] },
  { name: "recipient", label: "装机对象", type: "text" },
  { name: "appearance", label: "外观", type: "text" },
  { name: "notes", label: "补充说明", type: "textarea" },
];

export function RequirementFullEditor({ session, defaults, busy, onSave, onClose }: {
  session: Session;
  defaults: Map<string, unknown>;
  busy: boolean;
  // expectedRevision 是打开时的基线 revision:编辑期间 Session 刷新(他人/其他
  // 标签页改了需求)时保存必须按旧 revision 提交,由服务端 409 暴露冲突,
  // 而不是拿最新 revision 静默覆盖并发修改。
  onSave: (operations: RequirementOperation[], expectedRevision: number) => Promise<void>;
  onClose: () => void;
}) {
  // 打开时固定编辑基线(当时的状态快照与有效值预填):父组件重渲染、Session
  // 轮询刷新或 defaults 新建 Map 都不改写未保存输入;期间的服务端变化在保存时
  // 由 expected_revision 显式暴露(409 保留输入,不静默按新 revision 覆盖)。
  const [baseline] = useState(() => {
    const state = session.requirement_state;
    return state ? { state, values: initialValues(state, defaults) } : null;
  });
  const form = useForm<EditorValue>({ resolver: zodResolver(editorSchema), defaultValues: baseline?.values });
  const [error, setError] = useState<string | null>(null);
  if (!session.requirement_state || !baseline) return null;
  const save = form.handleSubmit(async (raw) => {
    const parsed = editorSchema.parse(raw);
    const operations = diffOperations(baseline.state, baseline.values, parsed);
    if (operations.length === 0) { onClose(); return; }
    try {
      setError(null);
      await onSave(operations, baseline.state.revision);
    } catch (cause) {
      // 保存失败保留输入;错误文案由 problem 映射给出。
      setError(userMessage(cause));
    }
  });

  return <form className="space-y-5 px-4 py-5 sm:px-6" onSubmit={(event) => void save(event)} data-testid="requirement-full-editor">
    <header>
      <p className="text-sm text-[var(--ink-muted)]">留空表示未指定，不会替代系统默认；保存后立即生效，确认前不会生成配置。</p>
    </header>
    <div className="grid gap-4 sm:grid-cols-2">
      {editorFields.map((field) => <Field key={field.name} label={field.label} hint={field.hint} error={form.formState.errors[field.name]?.message?.toString()}>
        {field.type === "select"
          ? <select aria-label={field.label} className={selectClass} disabled={busy} {...form.register(field.name)}>{field.options?.map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select>
          : field.type === "textarea"
            ? <Textarea aria-label={field.label} rows={3} disabled={busy} {...form.register(field.name)} />
            : <Input aria-label={field.label} type={field.type} step={field.name === "budget_flex" ? 0.01 : 1} disabled={busy} {...form.register(field.name)} />}
      </Field>)}
    </div>
    <fieldset><legend className="mb-2 text-sm text-[var(--ink-muted)]">已有配件（勾选并填写完整型号）</legend>
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">{(["cpu", "gpu", "motherboard", "memory", "ssd", "psu", "case", "cooler"] as const).map((category) => {
        const checked = form.watch("existing_parts").includes(category);
        return <label key={category} className="flex min-h-11 items-center gap-2 rounded-md border px-3 text-sm">
          <input type="checkbox" disabled={busy} checked={checked} onChange={(event) => {
            const current = form.getValues("existing_parts");
            const nextOwned = checked
              ? form.getValues("owned_parts").filter((part) => part.category !== category)
              : [...form.getValues("owned_parts"), { category, model: "", quantity: 1 }];
            form.setValue("owned_parts", nextOwned, { shouldDirty: true });
            form.setValue("existing_parts", event.target.checked ? [...current, category] : current.filter((item) => item !== category), { shouldDirty: true });
          }} />{categoryLabels[category]}
        </label>;
      })}</div>
      <Controller control={form.control} name="owned_parts" render={({ field }) => (
        <div className="mt-3 space-y-2">{field.value.map((part, index) => (
          <div key={`${part.category}-${index}`} className="flex flex-wrap items-end gap-2">
            <span className="w-20 text-xs text-[var(--ink-muted)]">{categoryLabels[part.category as keyof typeof categoryLabels] ?? part.category}</span>
            <Input aria-label={`${categoryLabels[part.category as keyof typeof categoryLabels] ?? part.category}完整型号`} placeholder="完整型号" className="min-w-48 flex-1" disabled={busy} value={part.model} onChange={(event) => field.onChange(field.value.map((item, current) => current === index ? { ...item, model: event.target.value } : item))} />
            <Button type="button" variant="ghost" size="sm" disabled={busy} onClick={() => {
              form.setValue("owned_parts", field.value.filter((_, current) => current !== index), { shouldDirty: true });
              form.setValue("existing_parts", form.getValues("existing_parts").filter((item) => item !== part.category), { shouldDirty: true });
            }}>移除</Button>
          </div>
        ))}{form.formState.errors.owned_parts && <p role="alert" className="text-xs status-fail">{form.formState.errors.owned_parts.message?.toString() ?? "请检查已有配件"}</p>}</div>
      )} />
    </fieldset>
    <fieldset><legend className="mb-2 text-sm text-[var(--ink-muted)]">优先投入</legend>
      <Controller control={form.control} name="priority" render={({ field }) => (
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">{(["cpu", "gpu", "motherboard", "memory", "ssd", "psu", "case", "cooler"] as const).map((category) => (
          <label key={category} className="flex min-h-11 items-center gap-2 rounded-md border px-3 text-sm">
            <input type="checkbox" disabled={busy} checked={field.value.includes(category)} onChange={(event) => field.onChange(event.target.checked ? [...field.value, category] : field.value.filter((item) => item !== category))} />{categoryLabels[category]}
          </label>
        ))}</div>
      )} />
    </fieldset>
    <p className="text-xs leading-5 text-[var(--ink-subtle)]">当前需求示例：{["budget_cny", "use_case.type"].map((key) => `${key}: ${activeValue(baseline.state, key) !== undefined ? requirementValue(key, activeValue(baseline.state, key)) : "未指定"}`).join(" · ")}</p>
    {error && <p role="alert" className="text-sm status-fail">{error} 当前输入已保留，请检查后重试。</p>}
    <div className="flex flex-wrap justify-end gap-2 border-t pt-4">
      <Button type="button" variant="ghost" disabled={busy} onClick={onClose}>取消</Button>
      <Button type="button" variant="outline" disabled={busy} onClick={() => void save()}><Save size={15} />{busy && <Loader2 className="animate-spin" size={15} />}保存修改</Button>
    </div>
  </form>;
}

function Field({ label, hint, error, children }: { label: string; hint?: string; error?: string; children: React.ReactNode }) {
  return <label className="block text-sm"><span className="mb-1.5 block text-[var(--ink-muted)]">{label}</span>{children}{hint && <span className="mt-1 block text-xs text-[var(--ink-subtle)]">{hint}</span>}{error && <span className="mt-1 block status-fail">{error}</span>}</label>;
}
