"use client";

import { useState, type ReactNode } from "react";
import type { Session } from "@/lib/api/types";
import { categoryLabels } from "@/lib/domain";
import { Button } from "./ui/button";
import { ProposalSources } from "./proposal-sources";

export function ProposalInspector({ proposal, children }: { proposal: NonNullable<Session["proposal"]>; children: ReactNode }) {
  const [showBuild, setShowBuild] = useState(proposal.result.outcome === "ready");
  const result = proposal.result;
  const title = result.outcome === "ready" ? "本次选配说明" : result.outcome === "technical_fault" ? "服务中断，进度已保存" : result.outcome === "clarify" || result.outcome === "collect" ? "候选与待确认信息" : "方案已保存，可继续调整";
  return <div className="flex h-full min-h-0 flex-col">
    <div className="flex shrink-0 gap-2 border-b px-4 py-2" aria-label="方案与配置切换">
      <Button variant={showBuild ? "ghost" : "outline"} size="sm" onClick={() => setShowBuild(false)}>{result.outcome === "ready" ? "选配说明" : "待解决方案"}</Button>
      <Button variant={showBuild ? "outline" : "ghost"} size="sm" onClick={() => setShowBuild(true)}>已确认配置</Button>
    </div>
    {showBuild ? <div className="min-h-0 flex-1">{children}</div> : <section aria-label="待解决方案" className="min-h-0 flex-1 space-y-5 overflow-y-auto p-4 text-sm">
      <header><h3 className="font-semibold">{title}</h3><p className="mt-2 text-[var(--ink-muted)]">{result.outcome === "ready" ? "配置已保存为新版本。" : "以下是本轮候选和待解决问题，尚未替换已确认配置。"}</p></header>
      {(result.issues ?? []).length > 0 && <div><h4 className="font-medium">还需要解决</h4><ul className="mt-2 list-disc space-y-2 pl-5 text-[var(--ink-muted)]">{result.issues.map((issue, i) => <li key={i}>{issue}</li>)}</ul></div>}
      {result.quote && <div className="border-y py-3"><p className="font-medium">{result.quote.missing_count ? "已知价格合计" : "参考总价"} ¥{result.quote.total_cny}</p><p className="mt-1 text-xs text-[var(--ink-muted)]">参考快照 {result.quote.snapshot_date || "日期未知"}{result.quote.missing_count > 0 ? ` · ${result.quote.missing_count} 项缺价，未计入合计` : ""} · 购买前请核价</p></div>}
      {(result.candidates ?? []).length > 0 && <div className="divide-y">{result.candidates.map((c) => <div key={c.id} className="py-3"><div className="flex justify-between gap-3 text-xs text-[var(--ink-muted)]"><span>{categoryLabels[c.category]}</span><span>{c.price_cny == null ? "价格未知" : `¥${c.price_cny}`}</span></div><p className="mt-2 font-medium">{c.model}</p>{c.external && <p className="mt-1 text-xs text-[var(--ink-muted)]">外部资料候选</p>}{(c.unknown ?? []).length > 0 && <p className="mt-2 text-xs text-[var(--ink-muted)]">待核验：{c.unknown?.join("、")}</p>}</div>)}</div>}
      {(result.assessments ?? []).length > 0 && <details className="border-t pt-3"><summary className="cursor-pointer">需求匹配与取舍</summary><p className="mt-2 text-xs text-[var(--ink-muted)]">以下为选型评估；性能建议不代表实测保证，硬件兼容性另见校验结果。</p><ul className="mt-3 space-y-3">{result.assessments?.filter((a) => proposal.requirement.requirement_state.fields[a.field]?.status === "active").map((a, i) => <li key={i}><span className="text-xs text-[var(--ink-muted)]">{proposal.requirement.requirement_state.fields[a.field]?.kind === "fact" ? "用途与场景" : proposal.requirement.requirement_state.fields[a.field]?.strength === "prefer" ? "软偏好" : "必须条件"} · {{ met: "匹配", unmet: "有偏差", unknown: "待核验" }[a.status]}</span><p className="mt-1">{a.explanation}</p></li>)}</ul></details>}
      {(result.assumptions ?? []).length > 0 && <details className="border-t pt-3"><summary className="cursor-pointer">本次选配假设</summary><p className="mt-2 text-xs text-[var(--ink-muted)]">以下是选配时的假设，未记录为你已表达的要求。</p><ul className="mt-2 list-disc space-y-2 pl-5">{result.assumptions.map((a, i) => <li key={i}>{a}</li>)}</ul></details>}
      <ProposalSources result={result} />
      {(result.model_calls != null || result.tokens != null) && <p className="text-xs text-[var(--ink-muted)]">本轮生成：{result.builder ? `${result.builder.provider}/${result.builder.model} · ` : ""}模型 {result.model_calls ?? "-"} 次 · 工具 {result.tool_calls ?? "-"} 次 · 搜索 {result.search_calls ?? "-"} 次 · 页面 {result.page_calls ?? "-"} 次 · {result.tokens ?? "-"} tokens · {(result.duration_ms ?? 0) / 1000 >= 1 ? `${Math.round((result.duration_ms ?? 0) / 1000)} 秒` : `${result.duration_ms ?? 0} 毫秒`}</p>}
      <p className="text-xs leading-5 text-[var(--ink-muted)]">继续在对话中补充信息或说明希望调整的地方。</p>
    </section>}
  </div>;
}
