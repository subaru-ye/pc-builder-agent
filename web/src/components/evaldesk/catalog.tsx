"use client";

import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, ArrowRight, BookOpen, GitCommitHorizontal, Search } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { evaldesk, runOutcome, runTime } from "@/lib/evaldesk/client";
import { getSuiteVersions, groupCommitRuns, groupSuiteRuns, latestRuns, UNRECORDED_SUITE_VERSION } from "@/lib/evaldesk/catalog";
import type { CommitRunGroup, SuiteRunGroup } from "@/lib/evaldesk/catalog";
import type { RunSummary, TimelineEntry } from "@/lib/evaldesk/types";
import styles from "./catalog.module.css";

export { getSuiteVersions, UNRECORDED_SUITE_VERSION } from "@/lib/evaldesk/catalog";

const dirtyText = (run: RunSummary) => run.versions.dirty == null ? "工作区状态未记录" : run.versions.dirty ? "有未提交改动" : "无未提交改动";
const versionName = (version: string | null) => version || "版本未记录";
const countText = (count: number | null | undefined) => count == null ? "冻结题数未记录" : `${count} 道冻结题目`;

function Time({ value }: { value: string | null }) {
  return <time dateTime={value ?? undefined}>{runTime(value)}</time>;
}

function useSavedTimeline() {
  return useQuery({ queryKey: ["evaldesk", "timeline"], queryFn: evaldesk.timeline, retry: false, staleTime: 30_000, refetchOnWindowFocus: false });
}

function SuiteSnapshot({ group, index, counts, pending, openSuite }: { group: SuiteRunGroup; index: number; counts: Map<string, number | null>; pending: boolean; openSuite: (id: string) => void }) {
  const preferred = group.runs.find(run => counts.get(run.id) != null) ?? group.runs[0];
  const [choice, setChoice] = useState("");
  const selected = group.runs.find(run => run.id === choice) ?? preferred;
  const count = counts.get(selected.id);
  return <li className={styles.snapshot} data-testid={`suite-snapshot-${index}`} data-suite-hash={group.hash ?? ""}>
    <div className={styles.rowTitle}><BookOpen size={18} aria-hidden="true" /><div><h3>内容记录 {index}</h3><p>{pending ? "正在核对冻结题数…" : countText(count)} · 关联 {group.runs.length} 次运行</p></div></div>
    {!group.hash && <p className={styles.notice}>内容指纹未记录，保留为独立记录，无法确认与其他运行的题库相同。</p>}
    <div className={styles.sourceRow}>
      <label className={styles.source}><span>从哪次运行查看题库</span><select aria-label={`内容记录 ${index} 的来源运行`} value={selected.id} onChange={event => setChoice(event.target.value)}>{group.runs.map(run => <option key={run.id} value={run.id}>{runTime(run.createdAt)} · {runOutcome(run)}</option>)}</select></label>
      <Button variant="outline" aria-label={`查看内容记录 ${index} 的完整题目`} onClick={() => openSuite(selected.id)}>查看完整题目<ArrowRight size={16} aria-hidden="true" /></Button>
    </div>
    <p className={styles.metadata}>所选来源：<Time value={selected.createdAt} />{!pending && count == null ? " · 该运行的冻结题库未记录或未通过校验" : ""}</p>
    <details className={styles.evidence}><summary>题库指纹与来源标识</summary><dl><div><dt>题库名称</dt><dd>{versionName(group.version)}</dd></div><div><dt>完整内容指纹</dt><dd>{group.hash ?? "未记录"}</dd></div><div><dt>所选产物</dt><dd>{selected.label}</dd></div></dl></details>
  </li>;
}

export function SuiteLibrary({ runs, version, onVersion, openSuite }: { runs: RunSummary[]; version: string; onVersion: (version: string) => void; openSuite: (run: string) => void }) {
  const timeline = useSavedTimeline();
  const [search, setSearch] = useState("");
  const counts = new Map((timeline.data?.items ?? []).map(entry => [entry.run.id, entry.frozenCaseCount]));
  const versions = getSuiteVersions(runs);
  const unknown = runs.filter(run => !run.versions.suite);
  const entries = [...versions.map(value => ({ value, label: value })), ...(unknown.length ? [{ value: UNRECORDED_SUITE_VERSION, label: "版本未记录" }] : [])];
  const matching = entries.filter(entry => entry.label.toLowerCase().includes(search.trim().toLowerCase()));
  const groups = version ? groupSuiteRuns(runs, version) : [];
  return <section className={styles.catalog} aria-label="评估集版本目录">
    <div className={styles.header}><div><h2>{version ? `评估集 · ${version === UNRECORDED_SUITE_VERSION ? "版本未记录" : version}` : "评估集版本"}</h2><p>{version ? "选择保存这份题库的运行，查看当时完整的输入与期望。" : "先选题库版本，再查看其中的题目。"}</p></div>{version ? <Button variant="outline" onClick={() => onVersion("")}><ArrowLeft size={16} aria-hidden="true" />返回版本目录</Button> : <label className={styles.search}><Search size={16} aria-hidden="true" /><span className="sr-only">搜索题库版本</span><input value={search} onChange={event => setSearch(event.target.value)} placeholder="版本名称或诊断版本" /></label>}</div>
    {timeline.error && <div className={styles.notice} role="status"><p>冻结题数暂时无法读取，仍可选择运行打开已保存的题库。</p><Button variant="ghost" onClick={() => void timeline.refetch()}>重新读取题数</Button></div>}
    {!version ? <ul className={styles.rows}>{matching.map(entry => {
      const snapshots = groupSuiteRuns(runs, entry.value);
      const related = latestRuns(snapshots.flatMap(group => group.runs));
      const knownCounts = [...new Set(related.flatMap(run => counts.get(run.id) == null ? [] : [counts.get(run.id)!]))];
      return <li key={entry.value} className={styles.versionRow} data-testid={`suite-version-${entry.value}`}><div><h3>{entry.label}</h3><p>{snapshots.length} 份内容记录 · {related.length} 次关联运行{timeline.isPending ? " · 正在读取题数…" : knownCounts.length === 1 ? ` · 已知 ${knownCounts[0]} 道题` : knownCounts.length > 1 ? " · 题数随内容记录不同" : " · 冻结题数未记录"}</p><p className={styles.metadata}>最近使用：<Time value={related[0]?.createdAt ?? null} /></p></div><Button variant="outline" aria-label={`打开题库版本 ${entry.label}`} onClick={() => onVersion(entry.value)}>查看版本<ArrowRight size={16} aria-hidden="true" /></Button></li>;
    })}</ul> : <><p className={styles.metadata}>{groups.length} 份内容记录 · {groups.reduce((total, group) => total + group.runs.length, 0)} 次关联运行</p><ul className={styles.rows}>{groups.map((group, index) => <SuiteSnapshot key={group.key} group={group} index={index + 1} counts={counts} pending={timeline.isPending} openSuite={openSuite} />)}</ul></>}
    {(!runs.length || (!version && !matching.length) || (version && !groups.length)) && <p className={styles.empty}>{!runs.length ? "暂无评估产物可用于建立题库目录。" : version ? "没有找到该版本的保存记录，请返回版本目录重新选择。" : "没有匹配的题库版本。"}</p>}
    <details className={styles.evidence}><summary>版本分组说明</summary><p>名称相同的版本按完整内容指纹分开；缺少指纹的运行各自保留。导航中的版本名称不证明题目相同。</p><p>题数来自已保存且通过校验的冻结题库；缺失题目不会用当前工作区的题库补齐。</p></details>
  </section>;
}

function CommitRecord({ group, openCommit, openSuite }: { group: CommitRunGroup; openCommit: (run: string) => void; openSuite: (run: string) => void }) {
  const [choice, setChoice] = useState("");
  const selected = group.entries.find(entry => entry.run.id === choice) ?? group.entries[0];
  const commit = group.entries.find(entry => entry.commit.status === "available")?.commit ?? group.entries[0].commit;
  const subject = commit.subject ?? "本机未找到该提交的说明";
  return <li className={styles.commitRecord} data-testid={`commit-record-${group.hash}`}>
    <div className={styles.rowTitle}><GitCommitHorizontal size={18} aria-hidden="true" /><div><h3>{subject}</h3><p>提交时间：<Time value={commit.committedAt} /> · 关联 {group.entries.length} 次评估</p></div></div>
    <div className={styles.sourceRow}><label className={styles.source}><span>查看哪个运行保存的提交证据</span><select aria-label={`关联运行：${subject}`} value={selected.run.id} onChange={event => setChoice(event.target.value)}>{group.entries.map(entry => <option key={entry.run.id} value={entry.run.id}>{runTime(entry.run.createdAt)} · {versionName(entry.run.versions.suite)} · {dirtyText(entry.run)}</option>)}</select></label><div className={styles.actions}><Button variant="outline" onClick={() => openCommit(selected.run.id)}>查看提交文件<ArrowRight size={16} aria-hidden="true" /></Button><Button variant="ghost" onClick={() => openSuite(selected.run.id)}>查看该运行题库</Button></div></div>
    <p className={styles.metadata}>所选评估：<Time value={selected.run.createdAt} /> · {dirtyText(selected.run)} · {countText(selected.frozenCaseCount)}</p>
    <details className={styles.evidence}><summary>提交完整标识</summary><code>{group.hash}</code></details>
  </li>;
}

function UnrecordedCommit({ entry, openSuite }: { entry: TimelineEntry; openSuite: (run: string) => void }) {
  return <li className={styles.versionRow} data-testid={`unrecorded-commit-${entry.run.id}`}><div><h4><Time value={entry.run.createdAt} /></h4><p>题库 {versionName(entry.run.versions.suite)} · {dirtyText(entry.run)}</p></div><Button variant="outline" onClick={() => openSuite(entry.run.id)}>查看该运行题库</Button></li>;
}

export function CommitHistory({ openCommit, openSuite }: { openCommit: (run: string) => void; openSuite: (run: string) => void }) {
  const timeline = useSavedTimeline();
  const [search, setSearch] = useState("");
  const { groups, unrecorded } = groupCommitRuns(timeline.data?.items ?? []);
  const query = search.trim().toLowerCase();
  const matches = (entry: TimelineEntry) => `${entry.commit.subject ?? ""} ${runTime(entry.commit.committedAt)} ${runTime(entry.run.createdAt)} ${entry.run.versions.suite ?? ""}`.toLowerCase().includes(query);
  const shown = groups.filter(group => group.entries.some(matches));
  const missing = unrecorded.filter(matches);
  return <section className={styles.catalog} aria-label="代码提交记录">
    <div className={styles.header}><div><h2>代码提交记录</h2><p>从提交说明进入文件变化，再查看关联评估。</p></div><label className={styles.search}><Search size={16} aria-hidden="true" /><span className="sr-only">搜索代码提交</span><input value={search} onChange={event => setSearch(event.target.value)} placeholder="提交说明、时间或题库版本" /></label></div>
    <p className={styles.metadata}>这里只列出评估产物关联的提交，按最近关联运行排列。</p>
    {timeline.isPending && <p className={styles.empty} role="status">正在读取已保存的提交与关联评估…</p>}
    {timeline.error && <div className={styles.empty} role="alert"><p>{timeline.error.message}</p><Button variant="outline" onClick={() => void timeline.refetch()}>重新读取提交</Button></div>}
    {timeline.data && <><p className={styles.metadata}>显示 {shown.length} 次提交{missing.length ? `；${missing.length} 次运行未记录有效提交` : ""}</p>{timeline.data.warnings.map((note, index) => <p key={index} className={styles.notice}>{note}</p>)}<ul className={styles.rows}>{shown.map(group => <CommitRecord key={group.hash} group={group} openCommit={openCommit} openSuite={openSuite} />)}</ul>{missing.length > 0 && <section className={styles.unrecorded} aria-label="提交未记录的运行"><h3>提交未记录的运行</h3><p className={styles.metadata}>这些运行无法归入某次代码提交，仍可查看保存的题库。</p><ul className={styles.rows}>{missing.map(entry => <UnrecordedCommit key={entry.run.id} entry={entry} openSuite={openSuite} />)}</ul></section>}{!shown.length && !missing.length && <p className={styles.empty}>{query ? "没有匹配的提交或关联运行。" : "暂无评估产物关联的提交记录。"}</p>}</>}
    <details className={styles.evidence}><summary>提交记录的来源与范围</summary><p>相同完整提交标识只展示一次；标题和提交时间由本机 Git 读取，关联运行来自评估元数据。</p><p>这是评估启动时的仓库提交，不包含当时未提交的改动，不能单独证明实际程序的完整源码。</p></details>
  </section>;
}
