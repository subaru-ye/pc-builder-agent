"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowLeft, ArrowRight, ArrowUp, Check, ChevronRight, CircleHelp, FlaskConical, RefreshCw, Search, X } from "lucide-react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { useTheme, type ThemePreference } from "@/components/theme-provider";
import { evaldesk, number, percent, delta, short, runTime, runCaption } from "@/lib/evaldesk/client";
import type { CaseComparison, CaseResponse, CaseScore, CaseSide, CaseStatus, CompareResponse, Metrics, RunSummary, Trial, Usage } from "@/lib/evaldesk/types";
import { ReadableChanges } from "./change-summary";
import { RunProvenance, RunTimeline } from "./provenance";
import { CommitHistory, SuiteLibrary } from "./catalog";
import { WorkbenchNavigation, type DeskSection } from "./navigation";
import styles from "./workbench.module.css";

const statusLabels: Record<CaseStatus, string> = { regressed: "退步", improved: "改善", persistent_failure: "持续失败", output_changed: "成绩不变 · 输出变化", unchanged: "未变化", added: "新增题", removed: "删除题", modified: "修改题", unavailable: "证据不足" };
const runLabels = { complete: "完整", incomplete: "未完成", legacy: "旧格式", invalid: "需检查" };
const roleLabels: Record<string, string> = { builder: "选配", screening: "初筛", embedding: "检索" };
const filters: (CaseStatus | "all" | "calls")[] = ["all", "regressed", "improved", "persistent_failure", "output_changed", "calls", "added", "removed", "modified", "unavailable", "unchanged"];
const filterName = (value: typeof filters[number]) => value === "all" ? "全部题目" : value === "calls" ? "调用变化" : statusLabels[value];

function State({ status }: { status: CaseStatus }) {
  const Icon = status === "regressed" ? ArrowDown : status === "improved" ? ArrowUp : status === "persistent_failure" ? X : status === "unavailable" ? CircleHelp : status === "unchanged" ? Check : ArrowRight;
  const color = status === "regressed" || status === "persistent_failure" ? "status-fail" : status === "improved" ? "status-pass" : status === "unavailable" ? "status-unknown" : "";
  return <span className={`${styles.state} ${color}`}><Icon size={14} aria-hidden="true" />{statusLabels[status]}</span>;
}

function UsageText({ usage }: { usage: Usage | null }) {
  if (!usage) return <span className="status-unknown">用量未记录</span>;
  const tokens = usage.usageResponses === 0 && usage.modelCalls > 0 ? "未知" : number(usage.totalTokens);
  return <div className={styles.usage}>
    <span>模型 <b>{number(usage.modelCalls)}</b> · 检索 {number(usage.embeddingCalls)}</span>
    <span>已知 token {tokens}</span>
    <small>用量响应 {number(usage.usageResponses)}/{number(usage.modelCalls)} · 记录 {usage.measuredRecords}/{usage.totalRecords}{!usage.complete ? "（部分）" : ""}</small>
  </div>;
}

function ErrorMessage({ error, retry }: { error: Error; retry: () => void }) {
  return <div className={styles.empty} role="alert"><p>{error.message}</p><Button variant="outline" onClick={retry}>重新读取</Button></div>;
}

function VersionDetails({ run }: { run: RunSummary }) {
  const v = run.versions;
  const items = [
    ["产物目录", run.label],
    ["题库清单", v.suiteHash], ["数据指纹", v.dataFingerprint], ["指纹来源", v.dataFingerprintSource],
    ["提示词版本", v.promptVersion], ["提示词原文快照", v.promptSnapshot ? "已记录" : "未记录"],
    ["代码提交", v.commit], ["工作区", v.dirty == null ? null : v.dirty ? "有未提交修改" : "干净"],
    ["实际程序", v.binary], ["源码清单", v.sourceFingerprint], ["原判卷版本", v.grader],
  ];
  return <details className={styles.versionDetails}><summary>版本证据与完整性</summary><dl>{items.map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value || "未记录"}</dd></div>)}</dl>
    <p>{run.verified ? "✓ 已离线核验冻结题目和原成绩" : "原成绩尚未完整复验"}</p>
    {run.notes.map((note, i) => <p key={i}>{note}</p>)}
  </details>;
}

function Score({ value }: { value: CaseScore | null }) { return <span className={styles.score}>{value ? `${value.passed}/${value.expected ?? value.total}` : "未记录"}</span>; }

function RunList({ runs, baseline, candidate, choose, inspect, provenance }: { runs: RunSummary[]; baseline: string; candidate: string; choose: (role: "baseline" | "candidate", id: string) => void; inspect: (id: string) => void; provenance: (id: string) => void }) {
  const [search, setSearch] = useState("");
  const filtered = runs.filter(run => `${run.label} ${runCaption(run)} ${run.versions.models.map(m => m.model).join(" ")}`.toLowerCase().includes(search.toLowerCase()));
  return <section aria-label="运行列表">
    <div className={styles.sectionHead}><div><h2>历史运行 <span>{runs.length}</span></h2><p>选择两次运行，查看变化与逐题证据。</p></div><label className={styles.search}><Search size={16} aria-hidden="true" /><span className="sr-only">搜索运行</span><input value={search} onChange={e => setSearch(e.target.value)} placeholder="日期、题库或模型" /></label></div>
    <div className={`${styles.runGrid} ${styles.columnHead}`} aria-hidden="true"><span>运行 / 时间</span><span>版本与条件</span><span>原成绩</span><span>调用与 token</span><span>选择</span></div>
    {filtered.map(run => <article key={run.id} className={`${styles.runGrid} ${styles.runRow}`} data-testid={`run-${run.label}`}>
      <div className={styles.runName}><button onClick={() => inspect(run.id)} title="查看本次运行题目"><time dateTime={run.createdAt ?? undefined}>{runTime(run.createdAt)}</time><ChevronRight size={14} aria-hidden="true" /></button><span className={run.status === "complete" ? styles.muted : "status-review"}>{runLabels[run.status]}{run.verified ? " · 已核验" : ""}</span><button className={styles.provenanceLink} onClick={() => provenance(run.id)}>查看题库<ChevronRight size={14} aria-hidden="true" /></button></div>
      <div><p><b>{run.versions.suite ?? "题库未记录"}</b> · 商品快照 {run.versions.snapshotDate ?? "未记录"}</p><p className={styles.muted}>提示词 {short(run.versions.promptVersion)} · 代码 {short(run.versions.commit)}</p><p className={styles.muted}>判卷 {run.versions.grader ?? "未记录"}</p><p className={styles.models}>{run.versions.models.length ? run.versions.models.map(m => `${roleLabels[m.role] ?? m.role} ${m.model}`).join(" · ") : "模型未记录"}</p><VersionDetails run={run} /></div>
      <div className={styles.outcome}><b>{percent(run.original.executionRate)}</b><span>执行通过 {run.original.passed}/{run.original.expected ?? run.original.recorded}</span><span>{run.original.allPassedRate == null ? "全部重复通过比例：未记录" : `全部 ${run.original.repeats} 次通过 ${run.original.allPassedCases}/${run.original.caseCount} · ${percent(run.original.allPassedRate)}`}</span>{run.status === "incomplete" && <span className="status-review">已记录 {run.original.recorded} 次，结果不完整</span>}</div>
      <UsageText usage={run.original.usage} />
      <div className={styles.rowActions}><Button variant={baseline === run.id ? "secondary" : "outline"} aria-pressed={baseline === run.id} onClick={() => choose("baseline", run.id)}>{baseline === run.id ? <Check /> : null}基线</Button><Button variant={candidate === run.id ? "secondary" : "outline"} aria-pressed={candidate === run.id} onClick={() => choose("candidate", run.id)}>{candidate === run.id ? <Check /> : null}候选</Button></div>
    </article>)}
    {!filtered.length && <div className={styles.empty}>{runs.length ? "没有匹配的运行，请更换搜索条件。" : "尚未找到评估产物。把已有运行保留在 artifacts/eval 或 artifacts/evalchange 后重新读取。"}</div>}
  </section>;
}

function MetricComparison({ data }: { data: CompareResponse }) {
  const a = data.metrics.baseline, b = data.metrics.candidate, d = data.metrics.delta;
  const passValue = (m: Metrics | null) => m ? `${m.passed}/${m.expected ?? m.recorded} · ${percent(m.executionRate)}` : "未记录";
  const allValue = (m: Metrics | null) => m?.allPassedRate != null ? `${m.allPassedCases}/${m.caseCount} · ${percent(m.allPassedRate)}` : "未记录";
  const points = (v: number | null) => v == null ? "未知" : delta(Number((v * 100).toFixed(2)), " 个百分点");
  const tokens = (u: Usage | null | undefined) => !u || (u.modelCalls > 0 && !u.usageResponses) ? "未知" : number(u.totalTokens);
  const rows = [
    ["执行通过", passValue(a), passValue(b), points(d.executionRate)],
    [`每题全部 ${a?.repeats ?? "?"} 次通过`, allValue(a), allValue(b), points(d.allPassedRate)],
    ["模型调用", number(a?.usage?.modelCalls), number(b?.usage?.modelCalls), delta(d.modelCalls)],
    ["检索调用", number(a?.usage?.embeddingCalls), number(b?.usage?.embeddingCalls), delta(d.embeddingCalls)],
    ["已知 token", tokens(a?.usage), tokens(b?.usage), delta(d.totalTokens)],
  ];
  return <section className={styles.metrics} aria-label="指标变化"><div className={styles.sectionHead}><div><h2>结果怎样</h2><p>{data.metrics.scope}</p></div></div>
    <div className={styles.metricGrid}><div className={styles.metricHeading}><span>指标</span><span>基线</span><span>候选</span><span>变化</span></div>{rows.map(([label, av, bv, change]) => <div key={label}><span>{label}</span><b>{av}</b><b>{bv}</b><span>{change}</span></div>)}</div>
    <p className={styles.footnote}>成绩统一按 {data.currentGrader} 复核。执行通过按每次重复计数；全部通过要求一题所有重复均通过，缺失记录不算通过。</p>
    <details className={styles.versionDetails}><summary>用量覆盖、耗时与原成绩</summary><div className={styles.sideBySide}><div><h3>基线原成绩</h3><p>{passValue(data.baseline.original)}；全部通过 {allValue(data.baseline.original)}</p><p>原判卷：{data.baseline.versions.grader ?? "未记录"} · 复核改变 {number(data.baseline.regradedTrials)} 次</p><UsageText usage={a?.usage ?? null} /><p>已知输入 / 输出：{number(a?.usage?.inputTokens)} / {number(a?.usage?.outputTokens)}</p></div><div><h3>候选原成绩</h3><p>{passValue(data.candidate.original)}；全部通过 {allValue(data.candidate.original)}</p><p>原判卷：{data.candidate.versions.grader ?? "未记录"} · 复核改变 {number(data.candidate.regradedTrials)} 次</p><UsageText usage={b?.usage ?? null} /><p>已知输入 / 输出：{number(b?.usage?.inputTokens)} / {number(b?.usage?.outputTokens)}</p></div></div><p>共同题累计耗时：{number(a?.durationMs)} → {number(b?.durationMs)} 毫秒，变化 {delta(d.durationMs)} 毫秒。</p><p>逻辑调用不含内部 HTTP 重试；token 不含未返回用量的调用及检索 token，不换算费用。耗时差异不直接代表代码效果。</p></details>
  </section>;
}

function Comparison({ data, openCase, provenance }: { data: CompareResponse; openCase: (id: string) => void; provenance: (id: string) => void }) {
  const [filter, setFilter] = useState<typeof filters[number]>("all");
  const [search, setSearch] = useState("");
  const unknown = data.conditions.filter(c => c.state === "unknown");
  const cases = data.cases.filter(c => (filter === "all" || (filter === "calls" ? c.callsDelta != null && c.callsDelta !== 0 : c.status === filter)) && `${c.id} ${c.title}`.toLowerCase().includes(search.toLowerCase()));
  const count = (f: typeof filters[number]) => f === "all" ? data.cases.length : f === "calls" ? data.cases.filter(c => c.callsDelta != null && c.callsDelta !== 0).length : data.counts[f];
  return <>
    <section className={styles.conditions} aria-label="条件变化"><div className={styles.sectionHead}><div><h2>改了什么</h2><p>{data.mode === "strict" ? "严格对照条件通过 · 统一口径离线复核" : data.mode === "observational" ? "条件对照 · 仅作观察比较" : "比较证据不足"}</p></div><span className={styles.muted}>共同题 {data.counts.common} 道</span></div>
      <ReadableChanges changes={data.changeSummaries} />
      <div className={styles.provenanceActions} aria-label="查看对照组资料"><Button variant="outline" onClick={() => provenance(data.baseline.id)}>基线题库<ChevronRight size={14} /></Button><Button variant="outline" onClick={() => provenance(data.candidate.id)}>候选题库<ChevronRight size={14} /></Button></div>
      <p className={styles.footnote}>原判卷：{data.baseline.versions.grader ?? "未记录（历史分支）"} → {data.candidate.versions.grader ?? "未记录（历史分支）"}；以下统一按 {data.currentGrader} 复核，原成绩另列。</p>
      {data.notices.find(n => n.startsWith("多项条件")) && <p className={styles.notice}>{data.notices.find(n => n.startsWith("多项条件"))}</p>}
      {data.strictReason && <p className={styles.notice}>{data.strictReason}</p>}
      <details className={styles.versionDetails}><summary>版本证据与比较口径 · 完整标识及 {unknown.length} 项缺失证据</summary>{data.notices.map((notice, i) => <p key={i}>{notice}</p>)}{data.conditions.map(c => <div key={c.key} className={styles.conditionDetail}><b>{c.label} · {c.state === "same" ? "相同" : c.state === "changed" ? "已变化" : "未记录 / 不足"}</b><p>基线：{c.baseline ?? "未记录"}</p><p>候选：{c.candidate ?? "未记录"}</p></div>)}</details>
      <div className={styles.filters} aria-label="变化速览">{(["regressed", "improved", "persistent_failure", "calls"] as const).filter(f => count(f) > 0).map(f => <button key={f} onClick={() => { setFilter(f); document.querySelector('[aria-label="逐题变化"]')?.scrollIntoView({ block: "start" }); }}>{filterName(f)} {count(f)} <ArrowRight size={14} className="inline" aria-hidden="true" /></button>)}</div>
    </section>
    <MetricComparison data={data} />
    <section aria-label="逐题变化"><div className={styles.sectionHead}><div><h2>点题目查看原因</h2><p>退步 {data.counts.regressed} · 改善 {data.counts.improved} · 持续失败 {data.counts.persistent_failure} · 同分输出变化 {data.counts.output_changed}</p></div><label className={styles.search}><Search size={16} /><span className="sr-only">搜索题目</span><input value={search} onChange={e => setSearch(e.target.value)} placeholder="题号或题目名称" /></label></div>
      <div className={styles.filters} aria-label="题目分类">{filters.filter(f => f === "all" || f === "calls" || count(f) > 0).map(f => <button aria-pressed={filter === f} key={f} onClick={() => setFilter(f)}>{filterName(f)} <span>{count(f)}</span></button>)}</div>
      <div className={`${styles.caseGrid} ${styles.columnHead}`} aria-hidden="true"><span>题目 / 变化</span><span>复核通过次数</span><span>模型调用变化</span><span>已知 token 变化</span></div>
      {cases.map(c => <CaseRow key={c.id} item={c} onClick={() => openCase(c.id)} />)}
      {!cases.length && <div className={styles.empty}>此分类没有题目。可切换分类或搜索题号。</div>}
    </section>
  </>;
}

function CaseRow({ item: c, onClick }: { item: CaseComparison; onClick: () => void }) {
  return <button className={`${styles.caseGrid} ${styles.caseRow}`} onClick={onClick} aria-label={`查看 ${c.id} ${c.title}`}>
    <span><span className={styles.caseTitle}><b>{c.id}</b> {c.title}</span><State status={c.status} />{c.outputChanges > 0 && <small className={styles.muted}> · {c.outputChanges} 次输出变化</small>}</span>
    <span><span className={styles.mobileLabel}>复核通过 </span><Score value={c.currentA} /> → <Score value={c.currentB} /><small className={styles.block}>原成绩 <Score value={c.originalA} /> → <Score value={c.originalB} /></small></span>
    <span><span className={styles.mobileLabel}>模型调用 </span><b>{delta(c.callsDelta)}</b><small className={styles.block}>{number(c.usageA?.modelCalls)} → {number(c.usageB?.modelCalls)}</small></span>
    <span><span className={styles.mobileLabel}>已知 token </span><b>{delta(c.tokensDelta)}</b><ChevronRight className={styles.rowChevron} size={16} /></span>
  </button>;
}

function TrialSummary({ trial, side }: { trial: Trial | undefined; side: CaseSide | null }) {
  if (!side || !trial) return <p className="status-unknown">此侧未记录该次执行。</p>;
  return <div className={styles.trialSummary}>
    <p>原成绩：{trial.original.dataError ? "数据不足" : trial.original.passed ? "✓ 通过" : "× 未通过"} · 统一复核：{trial.current ? trial.current.dataError ? "数据不足" : trial.current.passed ? "✓ 通过" : "× 未通过" : "未复核"}</p>
    <p>尝试次数 {number(trial.attempts)} · 耗时 {number(trial.durationMs)} 毫秒</p><UsageText usage={trial.usage} />
    {trial.error && <p className="status-fail">执行问题：{trial.error}</p>}
    {(trial.current?.failures ?? []).map((f, i) => <p className={styles.failure} key={i}><b>{f.id} · {f.name}</b><br />{f.detail}</p>)}
    {!!trial.original.failures.length && <details className={styles.versionDetails}><summary>原判卷失败证据</summary>{trial.original.failures.map((f, i) => <p key={i}>{f.id} · {f.name}：{f.detail}</p>)}</details>}
  </div>;
}

function TurnSide({ side, trial, turn }: { side: CaseSide | null; trial: Trial | undefined; turn: number }) {
  const input = side?.inputs.find(t => t.turn === turn), output = trial?.turns.find(t => t.turn === turn);
  return <div className={styles.turnSide}>
    <h4>冻结用户输入</h4><pre>{input?.input ?? "未记录"}</pre><details className={styles.expect} open><summary>冻结期望</summary><pre>{input?.expected ?? "未记录"}</pre></details>
    <h4>最终可见回复</h4><pre className={styles.reply}>{output ? output.output || "（已记录为空回复）" : "未记录"}</pre>
    {output?.failures.map((f, i) => <p className={styles.failure} key={i}><b>{f.id} · {f.name}</b><br />{f.detail}</p>)}
    {!!output?.modelOutputs.length && <details className={styles.versionDetails}><summary>模型原回复 / 格式重试（{output.modelOutputs.length} 次）</summary>{output.modelOutputs.map((text, i) => <div key={i}><h4>尝试 {i + 1}</h4><pre>{text || "（空回复）"}</pre></div>)}</details>}
  </div>;
}

function CaseDetail({ data, comparison, close }: { data: CaseResponse; comparison?: CompareResponse; close: () => void }) {
  const [selectedSeed, setSeed] = useState<number | null>(null);
  const section = useRef<HTMLElement>(null);
  useEffect(() => { section.current?.focus(); section.current?.scrollIntoView({ block: "start" }); }, [data.caseId]);
  const seeds = [...new Set([...(data.baseline?.trials ?? []).map(t => t.seed), ...(data.candidate?.trials ?? []).map(t => t.seed)])].sort((a, b) => a - b);
  const seed = selectedSeed ?? data.candidate?.trials.find(t => t.current && !t.current.passed)?.seed ?? seeds[0];
  const a = data.baseline?.trials.find(t => t.seed === seed), b = data.candidate?.trials.find(t => t.seed === seed);
  const turns = [...new Set([...(data.baseline?.inputs ?? []).map(t => t.turn), ...(data.candidate?.inputs ?? []).map(t => t.turn), ...(a?.turns ?? []).map(t => t.turn), ...(b?.turns ?? []).map(t => t.turn)])].sort((a, b) => a - b);
  const c = comparison?.cases.find(c => c.id === data.caseId);
  const failedTurns = [...new Set([...(a?.turns ?? []), ...(b?.turns ?? [])].filter(t => t.failures.length > 0).map(t => t.turn))];
  return <section className={styles.inspector} ref={section} tabIndex={-1} aria-label="单题详情">
    <div className={styles.sectionHead}><div><h2>{data.caseId} · {data.candidate?.title ?? data.baseline?.title}</h2><p>{c && <State status={c.status} />} · 原文与期望来自所选运行的冻结产物</p></div><Button variant="outline" onClick={close}><ArrowLeft />返回对比</Button></div>
    <div className={styles.filters} aria-label="重复次数">{seeds.map(s => <button key={s} aria-pressed={s === seed} onClick={() => setSeed(s)}>第 {s} 次重复</button>)}</div>
    {!!failedTurns.length && <div className={styles.filters} aria-label="失败轮次">{failedTurns.map(turn => <Button variant="outline" key={turn} onClick={() => { const node = document.getElementById(`eval-turn-${turn}`); node?.scrollIntoView({ block: "start" }); node?.focus({ preventScroll: true }); }}>定位第 {turn} 轮失败<ArrowRight /></Button>)}</div>}
    <div className={styles.sideBySide}><div><h3>基线 · 参考运行 <span>{comparison ? runCaption(comparison.baseline) : "运行信息未记录"}</span></h3>{data.baseline?.notes.map((n, i) => <p key={i} className={styles.notice}>{n}</p>)}<TrialSummary trial={a} side={data.baseline} /></div><div><h3>候选 · 待比较运行 <span>{comparison ? runCaption(comparison.candidate) : "运行信息未记录"}</span></h3>{data.candidate?.notes.map((n, i) => <p key={i} className={styles.notice}>{n}</p>)}<TrialSummary trial={b} side={data.candidate} /></div></div>
    {turns.map(turn => <section key={turn} id={`eval-turn-${turn}`} tabIndex={-1} className={styles.turn}><h3>第 {turn} 轮</h3><div className={styles.sideBySide}><div><span className={styles.mobileLabel}>基线</span><TurnSide side={data.baseline} trial={a} turn={turn} /></div><div><span className={styles.mobileLabel}>候选</span><TurnSide side={data.candidate} trial={b} turn={turn} /></div></div></section>)}
    {(a?.selection || b?.selection) && <section className={styles.turn}><h3>实际选件与交付结果</h3><div className={styles.sideBySide}><pre>{a?.selection ?? "未记录"}</pre><pre>{b?.selection ?? "未记录"}</pre></div></section>}
    <p className={styles.footnote}>用量为本次重复的合计。未单独记录的逐轮用量不分摊、不估算。这里仅展示可见回复与筛选后的证据。</p>
  </section>;
}

export function EvalWorkbench() {
  const params = useSearchParams();
  const queryClient = useQueryClient();
  const baseline = params.get("baseline") ?? "", candidate = params.get("candidate") ?? "", caseId = params.get("case") ?? "";
  const requestedView = params.get("view");
  // 旧的题库溯源链接继续可用，显示新的独立题目页面。
  const view = requestedView === "provenance" ? "suites" : ["compare", "suites", "commits", "timeline", "archive"].includes(requestedView ?? "") ? requestedView! : "runs";
  const comparing = view === "compare" && !!baseline && !!candidate;
  const showingList = view === "runs", showingSelection = showingList || view === "compare";
  const activeSection: DeskSection = view === "archive" ? "runs" : view as DeskSection;
  const runId = params.get("run") ?? "";
  const { theme, setTheme } = useTheme();
  const runs = useQuery({ queryKey: ["evaldesk", "runs"], queryFn: evaldesk.runs, retry: false, staleTime: 30_000, refetchOnWindowFocus: false });
  const compare = useQuery({ queryKey: ["evaldesk", "compare", baseline, candidate], queryFn: () => evaldesk.compare(baseline, candidate), enabled: comparing, retry: false, staleTime: 60_000, refetchOnWindowFocus: false });
  const detail = useQuery({ queryKey: ["evaldesk", "case", baseline, candidate, caseId], queryFn: () => evaldesk.case(baseline, candidate, caseId), enabled: comparing && !!caseId, retry: false, staleTime: 60_000, refetchOnWindowFocus: false });
  // 页面内筛选同步写入 URL，连续选择时始终保留上一次选择，并支持前进/后退。
  const navigate = (updates: Record<string, string | null>) => {
    const next = new URLSearchParams(window.location.search);
    Object.entries(updates).forEach(([key, value]) => value ? next.set(key, value) : next.delete(key));
    window.history.pushState(null, "", `/eval?${next}`);
    // 独立页面从标题开始；单题详情保留自身的证据定位，筛选也不跳回顶部。
    if (["view", "run", "version"].some(key => key in updates) && !next.get("case")) window.scrollTo(0, 0);
  };
  const version = params.get("version") ?? runs.data?.runs.find(r => r.id === runId)?.versions.suite ?? "";
  const switchSection = (section: DeskSection, selectedVersion?: string) => navigate({ view: section === "runs" ? null : section, run: null, question: null, case: null, version: section === "suites" ? selectedVersion ?? null : null });
  const provenance = (id: string) => navigate({ view: "suites", run: id, version: runs.data?.runs.find(r => r.id === id)?.versions.suite ?? null, question: null, case: null });
  const openCommit = (id: string) => navigate({ view: "commits", run: id, question: null, case: null });
  const provenanceActions = {
    run: runId, question: params.get("question") ?? "", selectQuestion: (id: string) => navigate({ question: id }),
    back: () => navigate({ view: view === "commits" ? "commits" : view === "archive" ? null : "suites", run: null, question: null, version: view === "suites" ? version : null }),
    backLabel: view === "commits" ? "返回提交记录" : view === "archive" ? "返回评估运行" : "返回题库版本",
    showResult: (id: string) => navigate({ baseline: runId, candidate: runId, view: "compare", case: id }),
    openSuite: () => provenance(runId), openCommit: () => openCommit(runId),
    openEvidence: () => navigate({ view: "archive", run: runId, case: null }), timeline: () => switchSection("timeline"),
  };
  const titles: Record<string, [string, string]> = { runs: ["评估运行", "查看历史成绩与用量，选择运行进行比较。"], compare: ["运行对比", "查看条件变化、结果增减与单题证据。"], suites: ["题库与评估集", "按版本查看冻结题目与期望。"], commits: ["代码提交记录", "查看历史提交及使用它的评估运行。"], timeline: ["溯源时间线", "按时间回看评估、题库与提交之间的关联。"], archive: ["运行版本档案", "查看这次运行记录的模型、数据与版本证据。"] };
  const captions = new Map<string, number>();
  runs.data?.runs.forEach(run => { const caption = runCaption(run); captions.set(caption, (captions.get(caption) ?? 0) + 1); });
  const optionCaption = (run: RunSummary) => { const caption = runCaption(run); return (captions.get(caption) ?? 0) > 1 ? `${caption} · 编号 ${run.id.slice(0, 8)}` : caption; };
  const runSelect = (role: "baseline" | "candidate") => <label className={styles.selectLabel}><span>{role === "baseline" ? "基线运行 · 作为参照" : "候选运行 · 查看变化"}</span><small>{role === "baseline" ? "选一次历史评估，作为比较的起点" : "选另一次评估，看它相对基线的变化"}</small><select aria-label={role === "baseline" ? "基线运行" : "候选运行"} aria-describedby="eval-selection-help" value={role === "baseline" ? baseline : candidate} onChange={e => navigate({ [role]: e.target.value, case: null })}><option value="">{role === "baseline" ? "选择作为参照的运行" : "选择要比较的运行"}</option>{runs.data?.runs.map(run => <option key={run.id} value={run.id} data-run-label={run.label}>{optionCaption(run)}</option>)}</select></label>;
  return <div className={styles.workspace}>
    <header className={styles.header}><Link href="/" className={styles.brand}><FlaskConical size={19} aria-hidden="true" /><span>装机配置单 <span className={styles.desktopOnly}>Agent</span></span></Link><span className={styles.readonly}>本机评估 · 只读</span><label className={styles.theme}><span className="sr-only">外观</span><select aria-label="外观" value={theme} onChange={e => setTheme(e.target.value as ThemePreference)}><option value="system">系统</option><option value="dark">深色</option><option value="light">浅色</option></select></label></header>
    <div className={styles.layout}><WorkbenchNavigation active={activeSection} version={version} runs={runs.data?.runs ?? []} navigate={switchSection} />
    <main className={styles.content}><div className={styles.titleRow}><div><h1 id="eval-page-heading" tabIndex={-1}>{titles[view][0]}</h1><p>{titles[view][1]}</p></div><Button variant="outline" onClick={() => void queryClient.invalidateQueries({ queryKey: ["evaldesk"] })}><RefreshCw size={15} />重新读取</Button></div>
      {showingSelection && <><div className={styles.selection}>{runSelect("baseline")}<ArrowRight className={styles.selectArrow} size={18} aria-hidden="true" />{runSelect("candidate")}<Button className={styles.compareButton} disabled={!baseline || !candidate} onClick={() => navigate({ view: "compare", case: null })}>对比运行<ArrowRight /></Button></div>
      <p id="eval-selection-help" className={styles.footnote}>比较的是两次评估当时的版本条件与结果。基线由你选择，不代表官方标准或最佳成绩；本页不会运行模型或更改基线。时间为北京时间。</p></>}
      {runs.isPending && <p className={styles.empty} role="status">正在读取本机产物与版本证据…</p>}
      {runs.error && <ErrorMessage error={runs.error} retry={() => void runs.refetch()} />}
      {runs.data?.warnings.map((n, i) => <p key={i} className={styles.notice}>{n}</p>)}
      {showingList && runs.data && <RunList runs={runs.data.runs} baseline={baseline} candidate={candidate} choose={(role, id) => navigate({ [role]: id })} inspect={id => navigate({ baseline: id, candidate: id, view: "compare", case: null })} provenance={provenance} />}
      {view === "timeline" && <RunTimeline baseline={baseline} candidate={candidate} open={provenance} openCommit={openCommit} choose={(role, id) => navigate({ [role]: id })} />}
      {view === "suites" && !runId && runs.data && <SuiteLibrary runs={runs.data.runs} version={version} onVersion={selected => switchSection("suites", selected)} openSuite={provenance} />}
      {view === "suites" && !!runId && <RunProvenance panel="suite" {...provenanceActions} />}
      {view === "commits" && !runId && <CommitHistory openCommit={openCommit} openSuite={provenance} />}
      {view === "commits" && !!runId && <section aria-label="代码提交记录"><RunProvenance panel="commit" {...provenanceActions} /></section>}
      {view === "archive" && <RunProvenance panel="evidence" {...provenanceActions} />}
      {view === "compare" && !comparing && <p className={styles.empty}>请选择基线和候选运行，再查看两次运行的变化。</p>}
      {comparing && compare.isPending && <p className={styles.empty} role="status">正在核验共同题、冻结条件与原成绩…</p>}
      {comparing && compare.error && <ErrorMessage error={compare.error} retry={() => void compare.refetch()} />}
      {comparing && compare.data && <Comparison key={`${baseline}-${candidate}`} data={compare.data} openCase={id => navigate({ case: id })} provenance={provenance} />}
      {comparing && caseId && detail.isPending && <p className={styles.empty} role="status">正在读取单题证据…</p>}
      {comparing && caseId && detail.error && <ErrorMessage error={detail.error} retry={() => void detail.refetch()} />}
      {comparing && caseId && detail.data && <CaseDetail key={`${baseline}-${candidate}-${caseId}`} data={detail.data} comparison={compare.data} close={() => { navigate({ case: null }); document.querySelector('[aria-label="逐题变化"]')?.scrollIntoView({ block: "start" }); }} />}
      <footer className={styles.footer}>原始日志与凭据不进入本页。历史缺失证据显示“未记录”；多因素变化与小样本波动不构成确定因果。</footer>
    </main></div>
  </div>;
}
