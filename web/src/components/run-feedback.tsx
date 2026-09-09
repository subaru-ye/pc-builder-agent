"use client";

import { useId, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ThumbsDown } from "lucide-react";
import { api } from "@/lib/api/client";
import { userMessage } from "@/lib/api/problem";
import type { FeedbackReason } from "@/lib/api/types";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";

const reasons: { value: FeedbackReason; label: string }[] = [
  { value: "unnecessary_question", label: "重复或不必要的追问" },
  { value: "requirement_mismatch", label: "没有理解我的需求" },
  { value: "configuration_issue", label: "配置有问题" },
  { value: "price_issue", label: "价格不准确" },
  { value: "unclear_explanation", label: "解释不清楚" },
  { value: "other", label: "其他" },
];

export function RunFeedback({ runID }: { runID: string }) {
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState<FeedbackReason>("unnecessary_question");
  const [comment, setComment] = useState("");
  const id = useId();
  const client = useQueryClient();
  const key = ["run-feedback", runID];
  const saved = useQuery({ queryKey: key, queryFn: () => api.getFeedback(runID), enabled: open });
  const submit = useMutation({
    mutationFn: () => api.submitFeedback(runID, reason, comment.trim()),
    onSuccess: (feedback) => { client.setQueryData(key, feedback); setOpen(false); },
  });
  const edit = () => {
    setReason(saved.data?.reason ?? "unnecessary_question");
    setComment(saved.data?.comment ?? "");
    submit.reset();
    setOpen(!open);
  };
  return <div className="mt-2">
    <Button variant="ghost" size="sm" className="min-h-11 text-xs text-[var(--ink-subtle)]" onClick={edit} aria-expanded={open} aria-controls={`${id}-form`}>
      <ThumbsDown size={14} aria-hidden />{saved.data ? "已反馈 · 修改" : "不满意"}
    </Button>
    {submit.isSuccess && !open && <span role="status" className="ml-2 text-xs text-[var(--ink-subtle)]">反馈已保存</span>}
    {open && <form id={`${id}-form`} className="mt-2 space-y-3 border-l pl-3" onSubmit={(event) => { event.preventDefault(); submit.mutate(); }}>
      <div><label htmlFor={`${id}-reason`} className="mb-1 block text-xs">哪里需要改进？</label>
        <select id={`${id}-reason`} value={reason} onChange={(event) => setReason(event.target.value as FeedbackReason)} className="min-h-11 w-full rounded-md border bg-[var(--surface-1)] px-3 text-sm focus-visible:outline-2 focus-visible:outline-[var(--primary)]">
          {reasons.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
        </select>
      </div>
      <div><label htmlFor={`${id}-comment`} className="mb-1 block text-xs">补充说明{reason === "other" ? "（必填）" : "（选填）"}</label>
        <Textarea id={`${id}-comment`} value={comment} maxLength={2000} onChange={(event) => setComment(event.target.value)} placeholder="例如：已经说明了分辨率，仍让我再确认。" />
      </div>
      {saved.data && <p className="text-xs text-[var(--ink-subtle)]">这次回复已有反馈。<button type="button" className="underline" onClick={() => { setReason(saved.data!.reason); setComment(saved.data!.comment); }}>载入已保存内容</button></p>}
      {(saved.isError || submit.isError) && <p role="alert" className="text-sm text-[var(--error)]">{userMessage(submit.error ?? saved.error)} 说明仍保留，可以重试。</p>}
      <div className="flex gap-2"><Button type="submit" variant="secondary" size="sm" className="min-h-11" disabled={submit.isPending || saved.isPending || (reason === "other" && !comment.trim())}>{submit.isPending ? "保存中…" : "提交反馈"}</Button><Button type="button" variant="ghost" size="sm" className="min-h-11" disabled={submit.isPending} onClick={() => setOpen(false)}>取消</Button></div>
    </form>}
  </div>;
}
