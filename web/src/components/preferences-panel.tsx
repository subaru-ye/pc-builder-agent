"use client";

// 跨会话偏好记忆(实验):查看、保存、改正、删除。唯一写入路径是
// "从当前会话选择字段";值与来源由服务端从会话需求状态核验提取,
// 前端只选择字段与归属对象。保存的偏好是以后会话的待确认建议,
// 当前对话永远优先;自动召回注入尚未实现。
import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { BookmarkPlus, Trash2 } from "lucide-react";
import { api } from "@/lib/api/client";
import { queryKeys } from "@/lib/api/query-keys";
import { userMessage } from "@/lib/api/problem";
import type { Preference, Session } from "@/lib/api/types";
import { Button } from "./ui/button";
import { requirementLabel, requirementValue } from "./requirement-status";

// 服务端会做完整核验,这里只预过滤明显不可保存的字段以减少必然失败的提交:
// 生效值 + 非不确定证据 + 非临时例外 + 对话来源(面板编辑值没有可核验的原话)。
function savableFields(session: Session): string[] {
  const fields = session.requirement_state?.fields ?? {};
  return Object.keys(fields).filter((key) => {
    const field = fields[key];
    return field.status === "active" && field.evidence !== "uncertain" &&
      field.scope !== "temporary" && field.source?.kind === "chat";
  });
}

function subjectLabel(subject: string) {
  return subject === "self" ? "本人" : `代配对象 · ${subject}`;
}

function preferenceTitle(preference: Preference) {
  const value = requirementValue(preference.field, preference.value);
  return `${requirementLabel(preference.field)}：${value}`;
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
      toast.success(action === "superseded" ? "偏好已更新，原偏好吗不再使用" : action === "unchanged" ? "这条偏好已经保存过" : "偏好已保存");
    },
    onError: (error) => toast.error(userMessage(error)),
  });
  const remove = useMutation({
    mutationFn: (id: string) => api.deletePreference(id, crypto.randomUUID()),
    onSuccess: () => { void refresh(); toast.success("偏好已删除，之后不会再出现"); },
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
  return <section aria-label="跨会话偏好记忆" className="border-t px-4 py-5 sm:px-6 [&_button]:min-h-11 lg:[&_button]:min-h-8">
    <header>
      <h2 className="text-base font-semibold">跨会话偏好记忆</h2>
      <p className="mt-1 text-xs text-[var(--ink-muted)]">
        实验功能：把这次会话里明确的偏好存起来，以后新会话会作为<b>待确认建议</b>出现，当前对话永远优先。
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

    {preferences.isLoading && <p className="mt-3 text-sm text-[var(--ink-muted)]">正在读取偏好…</p>}
    {preferences.isError && <p role="alert" className="mt-3 text-sm status-fail">{userMessage(preferences.error)}</p>}
    {!preferences.isLoading && list.length === 0 && !preferences.isError &&
      <p className="mt-3 text-sm text-[var(--ink-muted)]">还没有保存过偏好。上面对话确认过的偏好（如品牌、噪音、尺寸）可以选择保存。</p>}
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
