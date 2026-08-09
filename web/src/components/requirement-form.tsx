"use client";

import { zodResolver } from "@hookform/resolvers/zod";
import { Controller, useForm, useWatch } from "react-hook-form";
import { useEffect } from "react";
import { Loader2, Save } from "lucide-react";
import { Button } from "./ui/button";
import { Checkbox } from "./ui/checkbox";
import { Input } from "./ui/input";
import { Textarea } from "./ui/textarea";
import { categories, categoryLabels } from "@/lib/domain";
import { requirementSchema, type PartCategory, type RequirementFormValue, type RequirementSpec } from "@/lib/api/types";

const selectClass = "h-11 w-full rounded-md border bg-[var(--canvas)] px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--primary)]";

export function RequirementForm({ value, busy, onSave, onConfirm }: {
  value: RequirementSpec;
  busy: boolean;
  onSave: (value: RequirementSpec) => Promise<void>;
  onConfirm: (value: RequirementSpec, dirty: boolean) => Promise<void>;
}) {
  const form = useForm<RequirementFormValue>({
    resolver: zodResolver(requirementSchema),
    defaultValues: value,
  });
  const useCase = useWatch({ control: form.control, name: "use_case.type" });
  const existingParts = useWatch({ control: form.control, name: "existing_parts" });
  const priority = useWatch({ control: form.control, name: "priority" });
  useEffect(() => form.reset(value), [form, value]);

  const normalized = (raw: RequirementFormValue) => requirementSchema.parse(raw) as RequirementSpec;
  const save = form.handleSubmit(async (raw) => {
    await onSave(normalized(raw));
    form.reset(raw);
  });
  const confirm = form.handleSubmit(async (raw) => {
    const dirty = form.formState.isDirty;
    await onConfirm(normalized(raw), dirty);
    form.reset(raw);
  });
  const toggle = (field: "existing_parts" | "priority", category: PartCategory, checked: boolean) => {
    const current = form.getValues(field) ?? [];
    form.setValue(field, checked ? [...new Set([...current, category])] : current.filter((item) => item !== category), { shouldDirty: true });
  };

  return (
    <form className="space-y-6 p-4 sm:p-6" onSubmit={confirm}>
      <div>
        <h2 className="text-base font-semibold">确认装机需求</h2>
        <p className="mt-1 text-sm text-[var(--ink-muted)]">先核对和编辑需求，保存不会生成配置，确认后才开始调用模型。</p>
      </div>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="预算（元）" error={form.formState.errors.budget_cny?.message}>
          <Input type="number" min={1} step={1} {...form.register("budget_cny", { valueAsNumber: true })} />
        </Field>
        <Field label="预算弹性" error={form.formState.errors.budget_flex?.message}>
          <select className={selectClass} {...form.register("budget_flex", { valueAsNumber: true })}>
            <option value={0}>严格预算</option><option value={0.05}>±5%</option><option value={0.1}>±10%</option><option value={0.15}>±15%</option>
          </select>
        </Field>
        <Field label="主要用途">
          <select className={selectClass} {...form.register("use_case.type")}>
            <option value="gaming">游戏</option><option value="productivity">生产力</option><option value="general">日常综合</option>
          </select>
        </Field>
        <Field label="机箱尺寸">
          <select className={selectClass} {...form.register("size_pref")}>
            <option value="any">不限</option><option value="atx">ATX</option><option value="matx">M-ATX</option><option value="itx">ITX</option>
          </select>
        </Field>
        {useCase === "gaming" && <>
          <Field label="游戏（逗号分隔）" error={form.formState.errors.use_case?.titles?.message}>
            <Controller control={form.control} name="use_case.titles" render={({ field }) => <Input value={field.value?.join("，") ?? ""} onChange={(event) => field.onChange(event.target.value.split(/[，,]/).map((item) => item.trim()).filter(Boolean))} />} />
          </Field>
          <Field label="目标分辨率">
            <select className={selectClass} {...form.register("use_case.resolution")}><option value="1080p">1080p</option><option value="2K">2K</option><option value="4K">4K</option></select>
          </Field>
          <Field label="目标帧率">
            <Input type="number" min={1} {...form.register("use_case.fps_target", { setValueAs: (v) => v === "" ? undefined : Number(v) })} />
          </Field>
        </>}
        <Field label="噪音偏好">
          <select className={selectClass} {...form.register("noise_pref")}><option value="any">不限</option><option value="silent">尽量安静</option><option value="normal">常规</option></select>
        </Field>
        <Field label="CPU 品牌">
          <select className={selectClass} {...form.register("brand_pref.cpu")}><option value="any">不限</option><option value="intel">Intel</option><option value="amd">AMD</option></select>
        </Field>
        <Field label="显卡品牌">
          <select className={selectClass} {...form.register("brand_pref.gpu")}><option value="any">不限</option><option value="nvidia">NVIDIA</option><option value="amd">AMD</option></select>
        </Field>
      </div>
      <CheckGroup title="已有配件" values={existingParts ?? []} onToggle={(item, checked) => toggle("existing_parts", item, checked)} />
      <CheckGroup title="优先投入" values={priority ?? []} onToggle={(item, checked) => toggle("priority", item, checked)} />
      <Field label="补充说明" error={form.formState.errors.notes?.message}>
        <Textarea rows={3} {...form.register("notes")} />
      </Field>
      {form.formState.errors.root && <p role="alert" className="status-fail text-sm">{form.formState.errors.root.message}</p>}
      <div className="flex flex-wrap justify-end gap-2 border-t pt-4">
        <Button type="button" variant="outline" disabled={busy || !form.formState.isDirty} onClick={() => void save()}><Save size={15} />保存修改</Button>
        <Button type="submit" disabled={busy}>{busy && <Loader2 className="animate-spin" size={15} />}确认并生成配置</Button>
      </div>
    </form>
  );
}

function Field({ label, error, children }: { label: string; error?: string; children: React.ReactNode }) {
  return <label className="block text-sm"><span className="mb-1.5 block text-[var(--ink-muted)]">{label}</span>{children}{error && <span className="mt-1 block status-fail">{error}</span>}</label>;
}

function CheckGroup({ title, values, onToggle }: { title: string; values: PartCategory[]; onToggle: (item: PartCategory, checked: boolean) => void }) {
  return <fieldset><legend className="mb-2 text-sm text-[var(--ink-muted)]">{title}</legend><div className="grid grid-cols-2 gap-2 sm:grid-cols-4">{categories.map((category) => <label key={category} className="flex min-h-11 items-center gap-2 rounded-md border px-3"><Checkbox checked={values.includes(category)} onCheckedChange={(checked) => onToggle(category, checked === true)} /><span>{categoryLabels[category]}</span></label>)}</div></fieldset>;
}
