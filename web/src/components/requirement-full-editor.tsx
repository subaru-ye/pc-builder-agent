"use client";

// 完整需求抽屉(最大宽 672px):复杂字段与批量编辑。复用与侧栏相同的
// PATCH requirement-state operations 合同;未保存值只存在表单状态,
// 保存成功以服务端返回的完整 Session 更新缓存,409 保留输入。
import { zodResolver } from "@hookform/resolvers/zod";
import { Controller, useForm } from "react-hook-form";
import { useEffect } from "react";
import { Loader2, Save } from "lucide-react";
import type { RequirementOperation, RequirementState, Session } from "@/lib/api/types";
import { categoryLabels } from "@/lib/domain";
import { requirementValue } from "./requirement-status";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Textarea } from "./ui/textarea";
import { z } from "zod";

const selectClass = "h-11 w-full rounded-md border bg-[var(--canvas)] px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--primary)]";

const editorSchema = z.object({
  budget_cny: z.number().int().positive(),
  budget_flex: z.string(),
  budget_basis: z.string(),
  use_case_type: z.string(),
  use_case_titles: z.string(),
  use_case_resolution: z.string(),
  use_case_fps_target: z.string(),
  size_pref: z.string(),
  noise_pref: z.string(),
  brand_pref_cpu: z.string(),
  brand_pref_gpu: z.string(),
  existing_parts: z.array(z.string()),
  owned_parts: z.array(z.object({ category: z.string(), model: z.string(), quantity: z.number().int().min(1) })),
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
    budget_cny: Number(value("budget_cny") ?? 0),
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
function diffOperations(state: RequirementState, next: EditorValue): RequirementOperation[] {
  const operations: RequirementOperation[] = [];
  const current = (key: string) => activeValue(state, key);
  const set = (key: string, value: unknown, strength: "must" | "prefer", kind?: "fact" | "context" | "constraint") => {
    const previous = current(key);
    if (JSON.stringify(previous) === JSON.stringify(value)) return;
    operations.push({ op: "set", field: key, value, strength, ...(kind ? { kind } : {}), ...(key === "notes" || key === "recipient" || key === "appearance" ? { kind: kind ?? "context" } : {}) });
  };
  const remove = (key: string) => { if (current(key) !== undefined) operations.push({ op: "remove", field: key }); };
  const select = (key: string, raw: string, strength: "must" | "prefer" = "prefer") => {
    if (raw === "") remove(key); else set(key, raw, strength, "constraint");
  };
  set("budget_cny", next.budget_cny, "must");
  if (next.budget_flex === "") remove("budget_flex"); else set("budget_flex", Number(next.budget_flex), "prefer", "constraint");
  select("budget_basis", next.budget_basis, "must");
  select("use_case.type", next.use_case_type, "must");
  if (next.use_case_titles.trim() === "") remove("use_case.titles");
  else set("use_case.titles", next.use_case_titles.split(/[，,、\n]/).map((item) => item.trim()).filter(Boolean), "must", "fact");
  select("use_case.resolution", next.use_case_resolution, "must");
  if (next.use_case_fps_target === "") remove("use_case.fps_target"); else set("use_case.fps_target", Number(next.use_case_fps_target), "prefer", "fact");
  select("size_pref", next.size_pref);
  select("noise_pref", next.noise_pref);
  select("brand_pref.cpu", next.brand_pref_cpu);
  select("brand_pref.gpu", next.brand_pref_gpu);
  if (next.existing_parts.length === 0 && (current("existing_parts") as string[] | undefined)?.length) remove("existing_parts");
  else if (next.existing_parts.length > 0) set("existing_parts", [...next.existing_parts], "must", "fact");
  if (next.owned_parts.length === 0 && (current("owned_parts") as unknown[] | undefined)?.length) remove("owned_parts");
  else if (next.owned_parts.length > 0) set("owned_parts", next.owned_parts.map((part) => ({ category: part.category, model: part.model.trim(), quantity: part.quantity })), "must", "fact");
  if (next.priority.length === 0 && (current("priority") as string[] | undefined)?.length) remove("priority");
  else if (next.priority.length > 0) set("priority", [...next.priority], "prefer", "constraint");
  if (next.notes.trim() === "") remove("notes"); else set("notes", next.notes.trim(), "prefer", "context");
  if (next.recipient.trim() === "") remove("recipient"); else set("recipient", next.recipient.trim(), "prefer", "context");
  if (next.appearance.trim() === "") remove("appearance"); else set("appearance", next.appearance.trim(), "prefer", "context");
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
  onSave: (operations: RequirementOperation[]) => Promise<void>;
  onClose: () => void;
}) {
  const state = session.requirement_state;
  const form = useForm<EditorValue>({ resolver: zodResolver(editorSchema), defaultValues: state ? initialValues(state, defaults) : undefined });
  useEffect(() => { if (state) form.reset(initialValues(state, defaults)); }, [form, state, defaults]);
  if (!state) return null;
  const save = form.handleSubmit(async (raw) => {
    const parsed = editorSchema.parse(raw);
    const operations = diffOperations(state, parsed);
    if (operations.length === 0) { onClose(); return; }
    await onSave(operations);
  });

  return <form className="space-y-5 px-4 py-5 sm:px-6" onSubmit={(event) => void save(event)} data-testid="requirement-full-editor">
    <header>
      <h2 className="text-base font-semibold">编辑全部需求</h2>
      <p className="mt-1 text-sm text-[var(--ink-muted)]">留空表示未指定，不会替代系统默认；保存后立即生效，确认前不会生成配置。</p>
    </header>
    <div className="grid gap-4 sm:grid-cols-2">
      {editorFields.map((field) => <Field key={field.name} label={field.label} hint={field.hint} error={form.formState.errors[field.name]?.message?.toString()}>
        {field.type === "select"
          ? <select aria-label={field.label} className={selectClass} disabled={busy} {...form.register(field.name)}>{field.options?.map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select>
          : field.type === "textarea"
            ? <Textarea aria-label={field.label} rows={3} disabled={busy} {...form.register(field.name)} />
            : <Input aria-label={field.label} type={field.type} step={field.name === "budget_flex" ? 0.01 : 1} disabled={busy} {...form.register(field.name, field.type === "number" ? { setValueAs: (v) => v === "" ? "" : Number(v) } : undefined)} />}
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
        ))}</div>
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
    <p className="text-xs leading-5 text-[var(--ink-subtle)]">当前需求示例：{["budget_cny", "use_case.type"].map((key) => `${key}: ${activeValue(state, key) !== undefined ? requirementValue(key, activeValue(state, key)) : "未指定"}`).join(" · ")}</p>
    <div className="flex flex-wrap justify-end gap-2 border-t pt-4">
      <Button type="button" variant="ghost" disabled={busy} onClick={onClose}>取消</Button>
      <Button type="button" variant="outline" disabled={busy} onClick={() => void save()}><Save size={15} />{busy && <Loader2 className="animate-spin" size={15} />}保存修改</Button>
    </div>
  </form>;
}

function Field({ label, hint, error, children }: { label: string; hint?: string; error?: string; children: React.ReactNode }) {
  return <label className="block text-sm"><span className="mb-1.5 block text-[var(--ink-muted)]">{label}</span>{children}{hint && <span className="mt-1 block text-xs text-[var(--ink-subtle)]">{hint}</span>}{error && <span className="mt-1 block status-fail">{error}</span>}</label>;
}
