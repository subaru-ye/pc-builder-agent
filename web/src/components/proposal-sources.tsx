import type { Session } from "@/lib/api/types";
import { groupProposalSources, retrievalSummary, sourceKindLabels, sourceFieldLabels, type SourceGroup } from "@/lib/proposal-sources";

type Result = NonNullable<Session["proposal"]>["result"];

function SourceList({ groups, result }: { groups: SourceGroup[]; result: Result }) {
  const names = new Map(result.candidates.map((c) => [c.id, c.model]));
  return <ul className="mt-3 space-y-3">{groups.map((group) => <li key={group.key}>
    <details>
      <summary className="cursor-pointer break-words">{group.title || "资料来源"}<span className="ml-2 text-xs text-[var(--ink-muted)]">{group.entries.length} 条依据</span></summary>
      {/^https:\/\//.test(group.url) && <a href={group.url} target="_blank" rel="noopener noreferrer" className="mt-2 inline-block break-all text-xs text-[var(--primary)] underline">打开原始资料</a>}
      <ul className="mt-2 space-y-3">{group.entries.map((e) => <li key={e.id}>
        <p className="text-xs text-[var(--ink-muted)]">{sourceKindLabels[e.kind] ?? "已保存资料"} · {e.captured_at.slice(0, 10)}</p>
        <p className="mt-1 break-words text-xs">{e.candidate_id ? (names.get(e.candidate_id) ?? "其他候选") : e.kind === "catalog" ? "历史商品资料" : e.title}{e.field ? ` · ${sourceFieldLabels[e.field] ?? "规格资料"}` : ""}</p>
        <p className="mt-1 break-words text-xs leading-5 text-[var(--ink-muted)]">{e.text.slice(0, 240)}</p>
      </li>)}</ul>
    </details>
  </li>)}</ul>;
}

export function ProposalSources({ result }: { result: Result }) {
  const groups = groupProposalSources(result);
  const used = groups.filter((g) => g.used), other = groups.filter((g) => !g.used);
  return <section className="border-t pt-3" aria-label="方案资料来源">
    <p className="text-xs text-[var(--ink-muted)]">{retrievalSummary(result)}</p>
    <details className="mt-2">
      <summary className="cursor-pointer">查看方案来源 · {used.length} 个链接</summary>
      {used.length ? <SourceList groups={used} result={result} /> : <p className="mt-2 text-xs text-[var(--ink-muted)]">尚未关联具体来源；候选建议不等于已核验结论。</p>}
      {other.length > 0 && <details className="mt-3 border-t pt-3"><summary className="cursor-pointer text-xs text-[var(--ink-muted)]">其他检索资料 · {other.length} 个链接</summary><SourceList groups={other} result={result} /></details>}
      <p className="mt-3 text-xs text-[var(--ink-muted)]">本轮搜索尝试 {result.search_calls ?? "未记录"} 次，实际搜索请求 {result.search_requests ?? "未记录"} 次，网页读取尝试 {result.page_calls ?? "未记录"} 次。来源日期为资料获取时间。</p>
    </details>
  </section>;
}
