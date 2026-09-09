"use client";

import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, ArrowRight, BookOpen, GitCommitHorizontal, Search } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { evaldesk, number, runCaption, runOutcome, runTime } from "@/lib/evaldesk/client";
import type { CommitSummary, FrozenCase, ProvenanceResponse, RunSummary, TimelineEntry } from "@/lib/evaldesk/types";
import styles from "./provenance.module.css";

const roles: Record<string, string> = { builder: "选配", screening: "初筛", embedding: "检索" };
const statuses = { complete: "完整", incomplete: "未完成", invalid: "需检查", legacy: "旧格式" };
const stageName = (stage: string) => stage === "build" ? "选配 / 改单" : stage === "screening" ? "初筛 / 对话" : "阶段未记录";
const dirtyText = (run: RunSummary) => run.versions.dirty == null ? "工作区状态未记录" : run.versions.dirty ? "有未提交改动" : "无未提交改动";

function Time({ value }: { value: string | null }) {
  return <time dateTime={value ?? undefined}>{runTime(value)}</time>;
}

function CommitTitle({ commit }: { commit: CommitSummary }) {
  return <span>{commit.subject ?? (commit.status === "unrecorded" ? "代码提交未记录" : "本机未找到该提交的说明")}</span>;
}

function ReadFailure({ error, retry }: { error: Error; retry: () => void }) {
  return <div role="alert" className={styles.empty}><p>{error.message}</p><Button variant="outline" onClick={retry}>重新读取</Button></div>;
}

function TimelineRow({ entry, open, openCommit, choose, baseline, candidate }: { entry: TimelineEntry; open: (id: string) => void; openCommit: (id: string) => void; choose: (role: "baseline" | "candidate", id: string) => void; baseline: string; candidate: string }) {
  const r = entry.run;
  return <li data-testid={`timeline-${r.label}`} className={styles.timelineRow}>
    <div className={styles.runDate}><Time value={r.createdAt} /><span>{statuses[r.status]}{r.verified ? " · 已核验" : ""}</span></div>
    <div className={styles.event}>
      <h3>题库 {r.versions.suite ?? "未记录"} · {entry.frozenCaseCount == null ? "冻结题目未记录" : `${entry.frozenCaseCount} 道题`}</h3>
      <p>{runOutcome(r)} · 模型调用 {number(r.original.usage?.modelCalls)}</p>
      <p className={styles.muted}>记录的模型：{r.versions.models.length ? r.versions.models.map(m => `${roles[m.role] ?? m.role} ${m.model}`).join("；") : "未记录"}</p>
      <div className={styles.commitLine}><GitCommitHorizontal size={16} aria-hidden="true" /><div><p>运行时仓库提交：<CommitTitle commit={entry.commit} /></p>{entry.commit.committedAt && <p className={styles.muted}>提交时间 <Time value={entry.commit.committedAt} /></p>}<p className={styles.muted}>{dirtyText(r)}</p></div></div>
      <div className={styles.actions}><Button variant="outline" onClick={() => open(r.id)}><BookOpen size={16} />查看题库<ArrowRight size={16} /></Button><Button variant="ghost" onClick={() => openCommit(r.id)}>查看提交</Button><Button variant="ghost" aria-pressed={baseline === r.id} onClick={() => choose("baseline", r.id)}>{baseline === r.id ? "已选基线" : "选为基线"}</Button><Button variant="ghost" aria-pressed={candidate === r.id} onClick={() => choose("candidate", r.id)}>{candidate === r.id ? "已选候选" : "选为候选"}</Button></div>
    </div>
  </li>;
}

export function RunTimeline({ open, openCommit, choose, baseline, candidate }: { open: (id: string) => void; openCommit: (id: string) => void; choose: (role: "baseline" | "candidate", id: string) => void; baseline: string; candidate: string }) {
  const data = useQuery({ queryKey: ["evaldesk", "timeline"], queryFn: evaldesk.timeline, retry: false, staleTime: 30_000, refetchOnWindowFocus: false });
  const [search, setSearch] = useState("");
  const items = (data.data?.items ?? []).filter(e => `${runCaption(e.run)} ${e.run.versions.models.map(m => m.model).join(" ")} ${e.commit.subject ?? ""} ${e.run.label}`.toLowerCase().includes(search.toLowerCase()));
  const dated = items.filter(e => e.run.createdAt && !Number.isNaN(new Date(e.run.createdAt).getTime()));
  const undated = items.filter(e => !dated.includes(e));
  return <section className={styles.timeline} aria-label="溯源时间线">
    <div className={styles.sectionHead}><div><h2>溯源时间线</h2><p>按评估运行时间查看：用了哪些题、哪些模型、当时对应哪次提交。</p></div><label className={styles.search}><Search size={16} aria-hidden="true" /><span className="sr-only">搜索时间线</span><input placeholder="时间、题库、模型或提交说明" value={search} onChange={e => setSearch(e.target.value)} /></label></div>
    <p className={styles.help}>时间均为北京时间。提交时间单独标注；运行顺序不代表代码继承关系，记录的仓库提交也不包含当时未提交的改动。</p>
    {data.isPending && <p role="status" className={styles.empty}>正在读取运行与本机提交记录…</p>}
    {data.error && <ReadFailure error={data.error} retry={() => void data.refetch()} />}
    {data.data && <>{data.data.warnings.map((n, i) => <p className={styles.notice} key={i}>{n}</p>)}<p className={styles.help}>显示 {items.length} 次运行</p><ol className={styles.events}>{dated.map(e => <TimelineRow key={e.run.id} entry={e} {...{ open, openCommit, choose, baseline, candidate }} />)}</ol>{undated.length > 0 && <><h3 className={styles.undated}>时间未记录 · 无法放入先后顺序</h3><ol className={styles.events}>{undated.map(e => <TimelineRow key={e.run.id} entry={e} {...{ open, openCommit, choose, baseline, candidate }} />)}</ol></>}{!items.length && <p className={styles.empty}>{search ? "没有匹配的运行，请更换搜索条件。" : "暂无可读取的评估运行。"}</p>}{data.data.notes.map((n, i) => <p className={styles.help} key={i}>{n}</p>)}</>}
  </section>;
}

function FrozenQuestion({ item, opened, toggle, showResult }: { item: FrozenCase; opened: boolean; toggle: () => void; showResult: () => void }) {
  return <details className={styles.question} open={opened} data-testid={`frozen-${item.id}`}>
    <summary onClick={event => { event.preventDefault(); toggle(); }}><span><b>{item.id}</b> {item.title}</span><small>{stageName(item.stage)} · {item.inputs.length} 轮</small></summary>
    <div className={styles.questionBody}>
      {item.inputs.map(input => <section key={input.turn} className={styles.frozenTurn} aria-label={`冻结第 ${input.turn} 轮`}><h4>第 {input.turn} 轮</h4><div className={styles.frozenColumns}><div><h5>{input.inputKind === "text" ? "冻结用户输入" : "冻结需求与改单条件"}</h5><pre>{input.input || "（已记录为空输入）"}</pre></div><div><h5>期望表现</h5>{input.expectationSummary.length ? <ul>{input.expectationSummary.map((value, i) => <li key={i}>{value}</li>)}</ul> : <p className={styles.muted}>未记录可读期望摘要，展开查看完整记录。</p>}<details className={styles.evidence}><summary>完整期望记录</summary><pre>{input.expected}</pre></details></div></div></section>)}
      <p className={styles.help}>本题已记录 {item.recordedTrials} 次执行{item.plannedTrials == null ? "；计划次数未记录" : ` / 计划 ${item.plannedTrials} 次`}。</p>
      {item.notes.map((n, i) => <p key={i} className={styles.help}>{n}</p>)}
      <div className={styles.actions}><Button variant="outline" onClick={showResult}>查看本题运行结果<ArrowRight size={16} /></Button><details className={styles.evidence}><summary>题目内容指纹</summary><code>{item.contentHash ?? "未记录"}</code></details></div>
    </div>
  </details>;
}

type ProvenanceActions = {
  panel: "suite" | "commit" | "evidence"; question: string; selectQuestion: (id: string) => void;
  showResult: (id: string) => void; back: () => void; backLabel: string;
  openSuite: () => void; openCommit: () => void; openEvidence: () => void; timeline: () => void;
};

function ProvenanceContent({ data, panel, question, selectQuestion, showResult, back, backLabel, openSuite, openCommit, openEvidence, timeline }: { data: ProvenanceResponse } & ProvenanceActions) {
  const r = data.run, c = data.commit;
  const [search, setSearch] = useState("");
  const [stage, setStage] = useState("all");
  const root = useRef<HTMLElement>(null);
  useEffect(() => { root.current?.focus({ preventScroll: true }); }, [r.id, panel]);
  const filtered = data.cases.filter(q => (stage === "all" || q.stage === stage) && `${q.id} ${q.title} ${q.inputs.map(t => t.input).join(" ")}`.toLowerCase().includes(search.toLowerCase()));
  const evidence = [
    ["产物目录", r.label], ["题库版本", r.versions.suite], ["题库内容指纹", r.versions.suiteHash],
    ["代码提交", r.versions.commit], ["实际程序指纹", r.versions.binary], ["冻结源码指纹", r.versions.sourceFingerprint],
    ["提示词独立版本", r.versions.promptVersion], ["提示词原文快照", r.versions.promptSnapshot ? "已记录" : "未记录"],
    ["商品数据指纹", r.versions.dataFingerprint], ["数据指纹来源", r.versions.dataFingerprintSource],
  ];
  return <section ref={root} tabIndex={-1} className={styles.provenance} aria-label={panel === "suite" ? "题库与评估集" : panel === "commit" ? "提交详情" : "运行版本档案"}>
    <div className={styles.sectionHead}><div><h2>{panel === "suite" ? `题库 ${r.versions.suite ?? "版本未记录"}` : panel === "commit" ? "提交详情" : "运行版本档案"}</h2><p>来源运行 <Time value={r.createdAt} /> · {statuses[r.status]}</p></div><Button variant="outline" onClick={back}><ArrowLeft size={16} />{backLabel}</Button></div>
    <div className={styles.crossLinks} aria-label="关联资料">{panel !== "suite" && <Button variant="ghost" onClick={openSuite}><BookOpen size={16} />查看关联题库</Button>}{panel !== "commit" && <Button variant="ghost" onClick={openCommit}><GitCommitHorizontal size={16} />对应代码提交</Button>}{panel !== "evidence" && <Button variant="ghost" onClick={openEvidence}>运行版本档案</Button>}<Button variant="ghost" onClick={timeline}>返回时间线</Button></div>
    {panel !== "commit" && <details className={styles.evidence}><summary>资料来源与缺失说明</summary>{data.notes.map((n, i) => <p key={i}>{n}</p>)}</details>}
    {panel === "commit" && <section id="saved-commit" className={styles.commit} aria-label="代码提交">
      <h3>运行时的代码提交</h3><p className={styles.commitSubject}><CommitTitle commit={c} /></p>
      <dl className={styles.facts}><div><dt>提交时间</dt><dd><Time value={c.committedAt} /></dd></div><div><dt>运行时工作区</dt><dd>{dirtyText(r)}</dd></div></dl>
      <p className={styles.help}>此提交不包含运行时的未提交改动，也不能单独证明实际程序的完整源码。</p>
      <details className={styles.evidence}><summary>这次提交涉及的文件{c.filesAvailable ? ` · ${c.files.length} 项${c.filesTruncated ? "（部分）" : ""}` : " · 未记录 / 无法读取"}</summary>{c.filesAvailable ? <ul className={styles.files}>{c.files.map((file, i) => <li key={i}><span>{file.label}</span><code>{file.path}</code></li>)}</ul> : <p>本机无法读取提交文件清单；不会用当前工作区内容代替。</p>}{c.filesTruncated && <p>文件列表仅展示允许读取的部分，不能视作完整改动范围。</p>}{c.notes.map((n, i) => <p key={i}>{n}</p>)}</details>
      <details className={styles.evidence}><summary>完整提交标识</summary><code>{c.hash ?? "未记录"}</code></details>
    </section>}
    {panel === "evidence" && <section id="run-versions" className={styles.versions} aria-label="运行版本条件"><h3>这次运行使用的条件</h3><dl className={styles.facts}><div><dt>商品快照</dt><dd>{r.versions.snapshotDate ?? "未记录"}</dd></div><div><dt>原判卷</dt><dd>{r.versions.grader ?? "未记录"}</dd></div>{r.versions.models.map(model => <div key={model.role}><dt>{roles[model.role] ?? model.role}模型</dt><dd>{model.model}</dd></div>)}</dl><details className={styles.evidence}><summary>版本证据与完整标识</summary><dl className={styles.identifiers}>{evidence.map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value ?? "未记录"}</dd></div>)}</dl>{r.notes.map((n, i) => <p key={i}>{n}</p>)}</details></section>}
    {panel === "suite" && <section id="frozen-suite" className={styles.suite} aria-label="冻结题库">
      <div className={styles.sectionHead}><div><h3>冻结题库{data.frozenCaseCount == null ? "" : ` · ${data.frozenCaseCount} 道`}</h3><p>展开题目查看逐轮输入、期望，再进入该题的运行证据。</p></div><label className={styles.search}><Search size={16} aria-hidden="true" /><span className="sr-only">搜索冻结题目</span><input value={search} onChange={e => setSearch(e.target.value)} placeholder="题号、标题或输入内容" /></label></div>
      {data.frozenCaseCount == null ? <p className={styles.empty}>冻结题库未记录或未通过校验；无法还原完整题目，不以当前题库补齐。</p> : <><label className={styles.stage}>题目类型<select aria-label="题目类型" value={stage} onChange={e => setStage(e.target.value)}><option value="all">全部类型</option><option value="build">选配 / 改单</option><option value="screening">初筛 / 对话</option></select><span className={styles.help}>显示 {filtered.length} / {data.frozenCaseCount} 道</span></label>{filtered.map(item => <FrozenQuestion key={item.id} item={item} opened={question === item.id} toggle={() => selectQuestion(question === item.id ? "" : item.id)} showResult={() => showResult(item.id)} />)}{!filtered.length && <p className={styles.empty}>没有匹配的题目，请更换搜索内容或题目类型。</p>}</>}
    </section>}
  </section>;
}

export function RunProvenance({ run, ...actions }: { run: string } & ProvenanceActions) {
  const data = useQuery({ queryKey: ["evaldesk", "provenance", run], queryFn: () => evaldesk.provenance(run), enabled: !!run, retry: false, staleTime: 30_000, refetchOnWindowFocus: false });
  if (!run) return <div className={styles.empty}><p>请先选择一条记录，查看对应资料。</p><Button variant="outline" onClick={actions.back}>{actions.backLabel}</Button></div>;
  if (data.isPending) return <p role="status" className={styles.empty}>正在读取冻结题库与提交证据…</p>;
  if (data.error) return <ReadFailure error={data.error} retry={() => void data.refetch()} />;
  return <ProvenanceContent key={run} data={data.data} {...actions} />;
}
