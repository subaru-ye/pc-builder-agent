"use client";

// 跨会话偏好记忆(实验):保存、查看、改正、删除,以及召回确认。
// 保存:从当前会话选择字段,值与来源由服务端核验提取,前端只选字段与归属对象。
// 召回:新会话中用户显式选择归属(本人/具名代配对象)后载入建议,逐项确认才写入
// 当前需求(走服务端确认端点,当前会话已明确的值优先);未确认不产生任何效果,
// 也不向初筛/Builder 自动注入。与 internal/schemas 的稳定偏好白名单保持同步。
import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { BookmarkPlus, Check, Trash2 } from "lucide-react";
import { api } from "@/lib/api/client";
import { queryKeys } from "@/lib/api/query-keys";
import { userMessage } from "@/lib/api/problem";
import type { Preference, Session } from "@/lib/api/types";
import { Button } from "./ui/button";
import { requirementLabel, requirementValue } from "./requirement-status";

// 与 internal/schemas StablePreferenceFields 同步的最小白名单:
// 预算、已有件、一次性价格/规格与未分类 free.* 不是稳定偏好。
const stablePreferenceFields = new Set([
  "brand_pref.cpu", "brand_pref.gpu", "noise_pref", "size_pref", "appearance",
]);

function savableFields(session: Session): string[] {
  const fields = session.requirement_state?.fields ?? {};
  return Object.keys(fields).filter((key) => {
    const field = fields[key];
    return stablePreferenceFields.has(key) && field.status === "active" &&
      field.evidence !== "uncertain" && field.scope === "session" && field.source?.kind === "chat";
  });
}

function subjectLabel(subject: string) {
  return subject === "self" ? "本人" : `代配对象 · ${subject}`;
}

function preferenceTitle(preference: Preference) {
  return `${requirementLabel(preference.field)}：${requirementValue(preference.field, preference.value)}`;
}

const selectClass = "h-11 w-full rounded-md border bg-[var(--canvas)] px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--primary)]";

export function PreferencesPanel({ session }: { session: Session }) {
  const client = useQueryClient();
  const preferences = useQuery({ queryKey: queryKeys.preferences, queryFn: api.listPreferences });
  const refresh = () => client.invalidateQueries({ queryKey: queryKeys.preferences });
  const save = useMutation({
    mutationFn: ({ field, subject }: { field: string; subject: string }) => api.savePreference(session.id, field, subject),
    onSuccess: ({ action }) => {
      void refresh();
      toast.success(action === "superseded" ? "偏好已更新，原偏好不再使用" : action === "unchanged" ? "这条偏好已经保存过" : "偏好已保存");
    },
    onError: (error) => toast.error(userMessage(error)),
  });
  const remove = useMutation({
    mutationFn: (id: string) => api.deletePreference(id, crypto.randomUUID()),
    onSuccess: () => { void refresh(); toast.success("偏好已删除，之后不会再出现"); },
    onError: (error) => toast.error(userMessage(error)),
  });

  // 召回确认:显式选择归属后才读取建议;自定义归属必须把输入的名称
  // 作为真实 subject 传递(而非选择器占位值)。确认走服务端端点,
  // 当前会话已生效字段会被服务端跳过,历史偏好不会覆盖本轮需求。
  const [recallSubject, setRecallSubject] = useState("self");
  const [recallCustom, setRecallCustom] = useState("");
  const effectiveRecallSubject = recallSubject === "custom" ? recallCustom.trim() : recallSubject;
  const [recallOpen, setRecallOpen] = useState(false);
  const suggestions = useQuery({
    queryKey: [...queryKeys.preferences, "suggestions", session.id, effectiveRecallSubject],
    queryFn: () => api.preferenceSuggestions(session.id, effectiveRecallSubject),
    enabled: recallOpen && effectiveRecallSubject !== "",
  });
  const [picked, setPicked] = useState<Record<string, string>>({});
  const [dismissed, setDismissed] = useState<Record<string, boolean>>({});
  const resetRecallChoices = () => { setPicked({}); setDismissed({}); };
  const confirm = useMutation({
    mutationFn: (ids: string[]) => {
      const revision = session.requirement_state?.revision;
      if (revision === undefined) throw new Error("当前需求不可用");
      return api.confirmPreferences(session.id, { expected_revision: revision, subject: effectiveRecallSubject, memory_ids: ids }, crypto.randomUUID());
    },
    onSuccess: (result) => {
      client.setQueryData(queryKeys.session(session.id), result.session);
      // 建议随当前需求变化(已生效字段不再出现),列表键按归属前缀整体失效。
      void client.invalidateQueries({ queryKey: [...queryKeys.preferences, "suggestions"] });
      void client.invalidateQueries({ queryKey: queryKeys.preferences });
      toast.success(result.applied.length > 0 ? `已确认 ${result.applied.length} 项偏好` : "所选偏好都已生效或不可用，未做修改");
    },
    onError: (error) => toast.error(userMessage(error)),
  });

  const savable = useMemo(() => savableFields(session), [session]);
  const knownSubjects = useMemo(
    () => [...new Set((preferences.data ?? []).map((item) => item.subject).filter((subject) => subject !== "self"))],
    [preferences.data],
  );
  const [field, setField] = useState("");
  const [subject, setSubject] = useState("self");
  const [customSubject, setCustomSubject] = useState("");
  const effectiveSubject = subject === "custom" ? customSubject.trim() : subject;
  const canSave = field !== "" && effectiveSubject !== "" && effectiveSubject.length <= 40 && !save.isPending;

  const list = preferences.data ?? [];
  const suggestionList = (suggestions.data ?? []).filter((item) => !dismissed[item.choices[0]?.id ?? item.field]);
  const chosenIDs = suggestionList
    .map((item) => (item.status === "conflict" ? picked[item.field] : item.choices[0]?.id))
    .filter((id): id is string => !!id);

  return <section aria-label="跨会话偏好记忆" className="border-t px-4 py-5 sm:px-6 [&_button]:min-h-11 lg:[&_button]:min-h-8">
    <header>
      <h2 className="text-base font-semibold">跨会话偏好记忆</h2>
      <p className="mt-1 text-xs text-[var(--ink-muted)]">
        实验功能：把这次会话里明确的偏好（如品牌、静音、尺寸）存起来。在以后的会话打开这里、
        选择归属并<b>逐项确认</b>后才会写入当前需求；确认前不生效，确认后当前对话仍然优先，也不会自动注入配置生成。
      </p>
    </header>

    {savable.length > 0 && <div className="mt-4 space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        <select aria-label="选择要保存的偏好字段" className={`${selectClass} max-w-52 flex-1`} value={field} onChange={(event) => setField(event.target.value)}>
          <option value="">选择要保存的偏好…</option>
          {savable.map((key) => <option key={key} value={key}>{requirementLabel(key)}</option>)}
        </select>
        <select aria-label="偏好归属" className={`${selectClass} max-w-44 flex-1`} value={subject} onChange={(event) => setSubject(event.target.value)}>
          <option value="self">本人的偏好</option>
          {knownSubjects.map((item) => <option key={item} value={item}>{subjectLabel(item)}</option>)}
          <option value="custom">新建代配对象…</option>
        </select>
        <Button size="sm" disabled={!canSave}
          onClick={() => save.mutate({ field, subject: effectiveSubject })}>
          {save.isPending ? <BookmarkPlus className="animate-pulse" size={15} /> : <BookmarkPlus size={15} />}保存偏好
        </Button>
      </div>
      {subject === "custom" && <input
        className="h-11 w-full rounded-md border bg-[var(--canvas)] px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--primary)]"
        placeholder="给代配对象起个名字（最多 40 字，如：朋友小王）" maxLength={40}
        value={customSubject} onChange={(event) => setCustomSubject(event.target.value)} aria-label="代配对象名称" />}
      <p className="text-xs text-[var(--ink-subtle)]">想改正已保存的偏好：先在对话里说明新偏好，再在这里保存同一项；删除会连同历史记录一起移除。</p>
    </div>}

    <div className="mt-4 border-t pt-3">
      <div className="flex flex-wrap items-center gap-2">
        <select aria-label="选择要召回的归属" className={`${selectClass} max-w-52 flex-1`} value={recallSubject}
          onChange={(event) => { setRecallSubject(event.target.value); setRecallOpen(false); resetRecallChoices(); }}>
          <option value="self">本人的偏好</option>
          {knownSubjects.map((item) => <option key={item} value={item}>{subjectLabel(item)}</option>)}
          <option value="custom">代配对象（输入名称）…</option>
        </select>
        {recallSubject === "custom" && <input
          className="h-11 w-40 rounded-md border bg-[var(--canvas)] px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--primary)]"
          placeholder="代配对象名称" maxLength={40} value={recallCustom}
          onChange={(event) => { setRecallCustom(event.target.value); setRecallOpen(false); resetRecallChoices(); }} aria-label="召回的代配对象名称" />}
        <Button variant="outline" size="sm" disabled={effectiveRecallSubject === ""}
          onClick={() => { resetRecallChoices(); setRecallOpen(true); void suggestions.refetch(); }}>载入建议</Button>
      </div>
      {recallOpen && <>
        {suggestions.isLoading && <p className="mt-2 text-sm text-[var(--ink-muted)]">正在读取历史偏好…</p>}
        {suggestions.isError && <p role="alert" className="mt-2 text-sm status-fail">{userMessage(suggestions.error)}</p>}
        {suggestions.data && suggestionList.length === 0 &&
          <p className="mt-2 text-sm text-[var(--ink-muted)]">该归属下暂无可确认的历史偏好（当前会话已有的值不会重复建议）。</p>}
        {suggestionList.length > 0 && <ul className="mt-2 divide-y">
          {suggestionList.map((item) => {
            const chosen = item.status === "conflict" ? picked[item.field] : item.choices[0]?.id;
            return <li key={item.field} className="py-2.5">
              <div className="flex items-center gap-3">
                <div className="min-w-0 flex-1">
              {item.choices.map((choice) => <div key={choice.id}>
                <div className="flex items-baseline gap-2">
                  <label className="flex min-w-0 flex-1 items-baseline gap-2 text-sm">
                    <input type="radio" className="accent-[var(--primary)]" name={`pref-${item.field}`}
                      checked={chosen === choice.id} disabled={item.status !== "conflict"}
                      onChange={() => setPicked((prev) => ({ ...prev, [item.field]: choice.id }))} />
                    <span className="min-w-0 break-words">{preferenceTitle(choice)}</span>
                    <span className="shrink-0 text-xs text-[var(--ink-muted)]">
                      {choice.strength === "must" ? "必须满足" : "尽量满足"}
                    </span>
                  </label>
                  <span className="shrink-0 text-xs text-[var(--ink-subtle)]">
                    {new Date(choice.created_at).toLocaleDateString("zh-CN")}
                  </span>
                </div>
                {/* 每个候选值(冲突时多个)各自携带来源原话,供用户辨认出自哪个身份的哪句话。 */}
                <p className="mt-0.5 truncate text-xs text-[var(--ink-subtle)]" title={choice.source.quote}>
                  原话:{choice.source.quote}
                </p>
              </div>)}
                  {item.status === "conflict" && <p className="mt-0.5 text-xs status-review">不同身份的记录有冲突，请选一项或忽略</p>}
                </div>
                <div className="flex shrink-0 items-center gap-1">
                  <Button size="sm" variant="outline" disabled={confirm.isPending || !chosen}
                    aria-label={`确认偏好：${requirementLabel(item.field)}`}
                    onClick={() => chosen && confirm.mutate([chosen])}><Check size={14} />采用</Button>
                  <Button size="sm" variant="ghost" disabled={confirm.isPending} aria-label={`忽略偏好：${requirementLabel(item.field)}`}
                    onClick={() => setDismissed((prev) => ({ ...prev, [item.choices[0]?.id ?? item.field]: true }))}>忽略</Button>
                </div>
              </div>
            </li>;
          })}
        </ul>}
        {suggestionList.length > 1 && <Button size="sm" disabled={confirm.isPending || chosenIDs.length === 0}
          onClick={() => confirm.mutate(chosenIDs)}>确认所选 {chosenIDs.length} 项</Button>}
      </>}
    </div>

    {preferences.isLoading && <p className="mt-3 text-sm text-[var(--ink-muted)]">正在读取偏好…</p>}
    {preferences.isError && <p role="alert" className="mt-3 text-sm status-fail">{userMessage(preferences.error)}</p>}
    {!preferences.isLoading && list.length === 0 && !preferences.isError &&
      <p className="mt-3 text-sm text-[var(--ink-muted)]">还没有保存过偏好。</p>}
    {list.length > 0 && <ul className="mt-3 divide-y">
      {list.map((item) => <li key={item.id} className="flex items-center gap-3 py-2.5">
        <div className="min-w-0 flex-1">
          <p className="break-words text-sm">{preferenceTitle(item)}</p>
          <p className="mt-0.5 text-xs text-[var(--ink-subtle)]">{subjectLabel(item.subject)} · 保存于 {new Date(item.created_at).toLocaleDateString("zh-CN")} · 原话：{item.source.quote}</p>
        </div>
        <Button variant="ghost" size="sm" aria-label={`删除偏好：${preferenceTitle(item)}`} disabled={remove.isPending}
          onClick={() => remove.mutate(item.id)}><Trash2 size={14} />删除</Button>
      </li>)}
    </ul>}
  </section>;
}
