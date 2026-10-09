"use client";

import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, ArrowRight, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { reqv2, reqV2LayerLabels, reqV2Time, type BadCaseDetail, type BadCaseOccurrence, type BadCaseSummary, type ReqV2RunSummary } from "@/lib/evaldesk/reqv2";
import { groupRunExperiments, runPurposes } from "@/lib/evaldesk/run-experiments";
import { QuestionDesign } from "./datasets";
import styles from "./workbench.module.css";

type Navigate = (updates: Record<string, string | null>) => void;
export const badCaseStatus: Record<string, string> = { unanalysed: "未审阅", located: "已定位", pending: "待验证", verified: "该案例验证通过", recurred: "再次失败" };
const stateLabels: Record<string, string> = { failed: "未通过", passed: "通过", skipped: "已跳过 · 未评估", unevaluable: "门槛不可评估", incomplete: "证据不完整", missing: "引用未找到" };
const sideLabels: Record<string, string> = { screening: "Screening 初筛", builder: "Builder 选配", all: "整次运行", unknown: "对象未记录" };
const panels = [["overview", "问题概览"], ["evidence", "实际与期望"], ["analysis", "根因分析"], ["fixes", "修复记录"], ["validation", "验证结果"], ["history", "出现历史"]];
const catalogQuery = { queryKey: ["evaldesk", "reqv2", "bad-cases"], queryFn: reqv2.badCases, retry: false, staleTime: 30_000, refetchOnWindowFocus: false };

function referenceLabel(item: BadCaseSummary) {
  const ref = item.reference;
  if (item.kind === "run") return ref?.state === "passed" ? "验收参考门槛已满足" : "运行级问题 · 单独核对";
  if (!ref) return "验收参考未记录";
  if (ref.state === "passed") return `验收参考 ${ref.passed}/${ref.total} 通过`;
  if (ref.state === "failed") return `验收参考 ${ref.passed}/${ref.total} · 未全通过`;
  return ref.state === "unmatched" ? "未匹配验收参考" : "参考证据不足";
}

function ReferenceEvidence({ data, navigate }: { data: BadCaseSummary; navigate: Navigate }) {
  const ref = data.reference;
  return <section className={styles.turn} aria-label="验收参考结果"><h3>{referenceLabel(data)}</h3><p>{ref?.note || "未关联验收参考；人工处理状态与历史失败各自保留。"}</p>{ref && <Button variant="outline" onClick={() => navigate({ area: "runs", run: ref.run_id, layer: data.kind === "question" ? ref.layer : null, case: data.kind === "question" ? ref.case : null, panel: data.kind === "question" ? null : "gates", view: null, case_origin: data.id })}>{data.kind === "question" ? "查看参考题目与全部重复" : "查看参考运行门槛"}</Button>}</section>;
}

export function enterBadCases(params: URLSearchParams, navigate: Navigate, context: Record<string, string | null>) {
  navigate({ area: "bad-cases", run: null, case: null, layer: null, panel: null, view: null, case_origin: null, bad_id: null, bad_group: null, bad_bucket: "all", bad_panel: null, bad_occurrence: null, bad_fix: null, bad_pair: null, bad_repeat: null, bad_page: null, case_return: params.toString(), ...context });
}

export function BadCaseLinks({ params, navigate, run, layer, caseId, commit }: { params: URLSearchParams; navigate: Navigate; run?: string; layer?: string; caseId?: string; commit?: string }) {
  const catalog = useQuery(catalogQuery);
  const related = catalog.data?.cases.filter(item => commit ? item.commits.includes(commit) : item.occurrences.some(occurrence => occurrence.run?.id === run && occurrence.layer === layer && occurrence.case === caseId)) ?? [];
  const relatedIDs = new Set(related.map(item => item.id));
  const groups = catalog.data?.groups?.filter(group => group.members.some(member => relatedIDs.has(member))) ?? [];
  return <section className={`${styles.inlineActions} ${styles.caseActions}`} aria-label="关联案例">
    {groups.map(group => <Button variant="outline" key={group.id} onClick={() => enterBadCases(params, navigate, { bad_group: group.id, bad_id: group.members.find(member => relatedIDs.has(member))!, bad_run: run ?? null })}>查看案例与修复记录 · {group.title}</Button>)}
    {catalog.isPending && <span className={styles.muted}>正在读取案例关联…</span>}
    {catalog.error && <><span className={styles.muted}>案例关联暂不可读取</span><Button variant="outline" onClick={() => void catalog.refetch()}>重新读取案例关联</Button></>}
    {!catalog.isPending && !catalog.error && !related.length && <span className={styles.muted}>尚无关联失败或人工分析记录。</span>}
  </section>;
}

function State({ value }: { value: string }) {
  const tone = value === "passed" ? "status-pass" : value === "failed" ? "status-fail" : "status-unknown";
  return <span className={tone}>{stateLabels[value] ?? value}</span>;
}

function EvidenceReference({ value }: { value: string }) {
  const match = /^(docs\/(?!.*\.\.)[^@]+\.md)@([a-f0-9]{40})/.exec(value);
  return match ? <a className={styles.historyLink} href={`https://github.com/subaru-ye/pc-builder-agent/blob/${match[2]}/${match[1].split("/").map(encodeURIComponent).join("/")}`} target="_blank" rel="noreferrer">{value}</a> : <>{value}</>;
}

function readReturn(params: URLSearchParams, key: string): Record<string, string | null> {
  const query = new URLSearchParams(params.get(key) ?? "");
  const updates: Record<string, string | null> = {};
  // 仅恢复 /eval 自身的查询状态，不接受目标网址或文件路径。
  for (const name of new Set([...params.keys(), ...query.keys()])) updates[name] = query.get(name);
  return updates;
}

export function ReturnToBadCase({ params, navigate }: { params: URLSearchParams; navigate: Navigate }) {
  const id = params.get("case_origin");
  return id ? <Button variant="outline" onClick={() => navigate({ area: "bad-cases", bad_id: id, case_origin: null, run: null, case: null, layer: null, view: null, panel: null })}><ArrowLeft size={15} />返回案例</Button> : null;
}

function FrozenEvidence({ item, params, navigate }: { item: BadCaseOccurrence; params: URLSearchParams; navigate: Navigate }) {
  const enabled = !!item.run && !!item.case && !!item.layer;
  const evidence = useQuery({ queryKey: ["evaldesk", "reqv2", "case", item.run?.id, item.layer, item.case], queryFn: () => reqv2.case(item.run!.id, item.layer!, item.case!), enabled, retry: false, refetchOnWindowFocus: false });
  if (!enabled) return <section className={styles.turn}><h3>运行级事实</h3><State value={item.state} /><p>{item.detail}</p><p className={styles.muted}>此问题属于整次运行，不对应某道题的实际与期望。</p></section>;
  if (evidence.isPending) return <p role="status" className={styles.empty}>正在读取冻结逐题证据…</p>;
  if (evidence.error) return <div role="alert" className={styles.empty}><p>{evidence.error.message}</p><Button variant="outline" onClick={() => void evidence.refetch()}>重新读取逐题证据</Button></div>;
  const data = evidence.data!;
  const selected = Number(params.get("bad_repeat")) || item.repeat;
  const repeat = data.repeats.find(row => row.repeat === selected) ?? data.repeats.find(row => !row.pass && !row.skipped) ?? data.repeats[0];
  return <section aria-label="案例冻结证据">
    <p className={styles.muted}>{data.integrity.status === "complete" ? "运行证据完整" : `运行证据需核对：${data.integrity.notes.join("；")}`}</p>
    {data.frozen ? <section className={styles.turn} aria-label="冻结输入与期望"><h3>冻结输入与期望</h3><QuestionDesign question={data.frozen} /></section> : <p className={styles.notice}>冻结题目未记录或未通过校验：{data.frozen_note || "未记录"}</p>}
    <div className={styles.filters} aria-label="案例重复次数">{data.repeats.map(row => <button key={row.repeat} aria-pressed={row.repeat === repeat?.repeat} onClick={() => navigate({ bad_repeat: String(row.repeat) })}>第 {row.repeat} 次 · {row.skipped ? "已跳过" : row.pass ? "通过" : "未通过"}</button>)}</div>
    {!repeat ? <p className={styles.notice}>具体重复未记录。</p> : <section className={styles.turn} aria-label="实际操作与失败断言">
      <h3>第 {repeat.repeat} 次实际结果</h3>
      {repeat.skipped && <p className="status-unknown">已跳过 · {repeat.skipped}，不计为行为失败。</p>}
      {repeat.error && <p className="status-fail">{repeat.error}</p>}
      {!!repeat.vetoes?.length && <p className="status-fail">否决项：{repeat.vetoes.join("；")}</p>}
      <h4>失败断言</h4>{repeat.assertions.filter(assertion => !assertion.pass).map((assertion, index) => <div key={index} className={styles.datasetSection}><b>{assertion.name}</b><p>{assertion.detail || "细节未记录"}</p><small className={styles.muted}>{assertion.classification || "分类未记录"}</small></div>)}
      {!repeat.assertions.some(assertion => !assertion.pass) && <p className={styles.muted}>本次没有记录失败断言。</p>}
      <p className={styles.muted}>{item.turn ? `人工引用定位第 ${item.turn} 轮，依据见根因分析。` : "失败断言未记录具体轮次，以下逐轮核对；不推测定位。"}</p>
      {(repeat.turns ?? []).map(turn => <section key={turn.index} className={styles.datasetSection}><h4>第 {turn.index} 轮实际操作与回复</h4><p>{turn.reply || "回复未记录"}</p>{turn.operations?.length ? <ul>{turn.operations.map((operation, index) => <li key={index}><code>{operation}</code></li>)}</ul> : <p className={styles.muted}>实际操作未记录。</p>}<details className={styles.versionDetails} open={item.turn === turn.index}><summary>操作、信号与调用事实</summary><pre>{JSON.stringify({ operations: turn.operations, turn_signals: turn.turn_signals, model_called: turn.screen_model_called }, null, 2)}</pre></details></section>)}
      {repeat.observation_rest && <details className={styles.versionDetails}><summary>最终状态与原始观测</summary><pre>{JSON.stringify(repeat.observation_rest, null, 2)}</pre></details>}
      <Button variant="outline" onClick={() => navigate({ area: "runs", run: item.run!.id, layer: item.layer!, case: item.case!, case_origin: params.get("bad_id"), view: null })}>查看原逐题详情<ArrowRight size={15} /></Button>
    </section>}
  </section>;
}

function CaseInspector({ data, params, navigate }: { data: BadCaseDetail; params: URLSearchParams; navigate: Navigate }) {
  const review = data.review;
  const panel = panels.some(([key]) => key === params.get("bad_panel")) ? params.get("bad_panel")! : "overview";
  const selectedRun = params.get("bad_run") || review?.references[0]?.run_name;
  const item = data.occurrences.find(occurrence => occurrence.id === params.get("bad_occurrence")) ?? data.occurrences.find(occurrence => occurrence.state === "failed" && (occurrence.run?.id === selectedRun || occurrence.run_name === selectedRun)) ?? data.occurrences.find(occurrence => occurrence.state === "failed") ?? data.occurrences[0];
  const fix = review?.fixes?.find(value => value.id === params.get("bad_fix")) ?? review?.fixes?.[0];
  const pairs = fix ? data.verifications.filter(pair => fix.validations.some(value => value.id === pair.id && value.baseline === pair.baseline && value.candidate === pair.candidate)) : data.verifications;
  const pair = pairs.find(value => value.id === params.get("bad_pair")) ?? pairs[0];
  const openRun = (id: string) => navigate({ area: "runs", run: id, layer: null, case: null, panel: "evidence", view: null, case_origin: data.id });
  const sources = (values: { reference: string; excerpt: string }[] = []) => <details className={styles.versionDetails}><summary>来源依据</summary>{values.length ? values.map((source, index) => <div className={styles.reviewEvidence} key={index}><p><EvidenceReference value={source.reference} /></p><blockquote>{source.excerpt}</blockquote></div>) : <p>未记录</p>}</details>;
  return <section aria-label="案例详情" className={styles.inspector}>
    {data.notes.map((note, index) => <p className={styles.notice} key={index}>{note}</p>)}
    <div className={styles.sectionHead}><div><h2>{data.title}</h2><p>{sideLabels[data.side]} · {data.conclusion || badCaseStatus[data.status]}</p></div><Button variant="outline" onClick={() => navigate({ bad_id: null, bad_group: null, bad_panel: null, bad_occurrence: null, bad_repeat: null, bad_fix: null, bad_pair: null })}><ArrowLeft size={15} />返回案例列表</Button></div>
    <nav className={styles.filters} aria-label="案例详情分区">{panels.map(([key, label]) => <button key={key} aria-current={panel === key ? "page" : undefined} onClick={() => navigate({ bad_panel: key })}>{label}</button>)}</nav>
    {["validation", "history"].includes(panel) && <ReferenceEvidence data={data} navigate={navigate} />}
    {panel === "overview" && <section className={styles.turn}>
      <h3>{data.conclusion || badCaseStatus[data.status]}</h3><p>{data.summary}</p>
      {data.category === "open" && pair && !pair.checked && <section aria-label="待处理原因"><h4>为什么仍待处理</h4>{pair.notes.map((note, index) => <p key={index}>{note}</p>)}<Button variant="outline" onClick={() => navigate({ bad_panel: "validation" })}>查看验证缺口</Button></section>}
      {data.category === "history" && <p>此项保留历史或探索事实，不作为当前已确认的待修复问题；参考记录与历次结果见验证和历史分区。</p>}
      {data.category === "gaps" && <p>尚无足够的同版本验收证据判断当前结果；查看原运行和版本差异后补齐依据。</p>}
      {review && <p className={styles.muted}>人工审阅：{reqV2Time(review.reviewed_at)} · {review.confirmed ? "根因已确认" : "根因待确认"}</p>}
      <p className={styles.muted}>{data.run_ids.length} 次关联运行；历史失败与模型探索分别保留。</p>
      {item && <Button variant="outline" onClick={() => navigate({ bad_panel: "evidence", bad_occurrence: item.id })}>查看来源记录的实际与期望<ArrowRight size={15} /></Button>}
      {(review?.unknown ?? []).map((note, index) => <p className={styles.notice} key={index}>{note}</p>)}
    </section>}
    {panel === "evidence" && <>
      <label className={styles.selectLabel}>出现记录<select aria-label="案例出现记录" value={item?.id ?? ""} onChange={event => navigate({ bad_occurrence: event.target.value, bad_repeat: null })}>{data.occurrences.map(occurrence => <option key={occurrence.id} value={occurrence.id}>{occurrence.run_name} · 第{occurrence.repeat || "未记录"}次 · {stateLabels[occurrence.state]}</option>)}</select></label>
      {item && <><p className={styles.scopeNote}>{item.context || "未经人工关联的冻结事实"}</p><FrozenEvidence key={item.id} item={item} params={params} navigate={navigate} /></>}
    </>}
    {panel === "analysis" && <section className={styles.turn}><h3>根因分析 · {review?.confirmed ? "已确认" : "待确认"}</h3><p>{review?.root_cause || "待分析：未记录已确认根因，不从失败断言生成修复结论。"}</p>{sources(review?.sources)}<h4>仍未知的部分</h4>{review?.unknown?.length ? review.unknown.map((note, index) => <p key={index}>{note}</p>) : <p className={styles.muted}>分析边界未记录。</p>}</section>}
    {panel === "fixes" && <section className={styles.turn}><h3>修复记录</h3>{!fix ? <p>未记录修改或回归测试依据。</p> : <>
      <div className={styles.filters} aria-label="修复尝试">{review!.fixes.map(value => <button key={value.id} aria-pressed={fix.id === value.id} onClick={() => navigate({ bad_fix: value.id, bad_pair: null })}>{value.id}</button>)}</div>
      <dl className={styles.evidence}><div><dt>为什么改</dt><dd>{fix.reason}</dd></div><div><dt>改了什么</dt><dd>{fix.changes}</dd></div><div><dt>修改类型</dt><dd>{fix.combination ? "组合修改：提示词与代码，不能作提示词单因素归因" : fix.prompt_commit ? "提示词修改；其他代码变化仍需核对" : "代码修复；未关联提示词改动"}</dd></div></dl>
      <div className={styles.inlineActions}><a className={styles.historyLink} href={`https://github.com/subaru-ye/pc-builder-agent/commit/${fix.commit}`} target="_blank" rel="noreferrer">查看代码提交与 diff</a>{fix.prompt_commit && <Button variant="outline" onClick={() => navigate({ area: "prompts", run: null, view: null, case_origin: data.id, prompt_role: fix.prompt_role!, prompt_view: "compare", prompt_version: `git:${fix.prompt_commit}:${fix.prompt_role}`, prompt_base: fix.prompt_base ? `git:${fix.prompt_base}:${fix.prompt_role}` : null, prompt_component: "system" })}>查看提示词原文 diff</Button>}</div>
      {!fix.prompt_commit && <p className={styles.muted}>未记录提示词版本，不把代码提交标成提示词改动。</p>}
      <h4>回归测试与证据</h4>{fix.tests?.length ? fix.tests.map(test => <div className={styles.datasetSection} key={test.name}><b>{test.name}</b><p>{test.result === "recorded_pass" ? "历史来源记载执行通过" : test.result === "tests_added" ? "仅新增测试，未确认执行结果" : "执行结果未知"}</p><p className={styles.muted}><EvidenceReference value={test.evidence} /></p></div>) : <p>未记录</p>}
      {sources(fix.sources)}<details className={styles.versionDetails}><summary>完整代码与提示词身份</summary><p>代码提交：{fix.commit}</p><p>提示词源码：{fix.prompt_commit || "未记录"}</p><p>代码身份不代替运行保存原文；dirty 或历史原文缺失时不能据此重建实际请求。</p></details>
    </>}</section>}
    {panel === "validation" && <section className={styles.turn}><h3>验证结果</h3>{!pair ? <p>未记录所选基线、候选和验收范围。</p> : <>
      <label className={styles.selectLabel}>验证对<select aria-label="案例验证对" value={pair.id} onChange={event => navigate({ bad_pair: event.target.value })}>{pairs.map(value => <option key={value.id} value={value.id}>{value.id} · {value.scope}</option>)}</select></label>
      <p>{pair.checked ? pair.validation_kind === "historical" ? "历史修复与候选验收已核对（不作为同模型配对实验）" : "该案例验证通过（已核对适用重复与身份）" : "该案例验证尚未确认"}；发布验证独立，不据此宣称可以发布。</p><p>{pair.scope}</p><p>{pair.conclusion}</p>
      {pair.notes.map((note, index) => <p className={styles.notice} key={index}>{note}</p>)}{pair.limitations.map((note, index) => <p className={styles.notice} key={index}>{note}</p>)}
      <div className={styles.sideBySide}>{[[pair.baseline_run, pair.baseline_case, "修复前"], [pair.candidate_run, pair.candidate_case, "修复后"]].map(([runValue, caseValue, label], index) => {
        const run = runValue as typeof pair.baseline_run, evidence = caseValue as typeof pair.baseline_case;
        return <section key={index}><h4>{String(label)}</h4><p>{run?.dir_name ?? "准确运行未找到"}</p><p>目标重复：{evidence?.repeats.map(row => `${row.repeat} ${row.skipped ? "已跳过" : row.pass ? "通过" : "未通过"}`).join(" · ") || "未记录"}</p><p>整批总门槛：{run?.gate_passed == null ? "未记录" : run.gate_passed ? "通过" : "未通过"}</p>{run?.layer_scores?.conversations && <p>多轮题目：{run.layer_scores.conversations.passed}/{run.layer_scores.conversations.cases}</p>}<details className={styles.versionDetails}><summary>运行代码与模型身份</summary><p>代码提交：{run?.code_commit || "未记录"}</p><p>模型配置核对：{pair.model_same ? "一致" : "不同或未记录"}</p><p>其他代码变化须结合完整提交 diff 与修复来源核对，不从前后成绩推断单因素因果。</p></details><p>证据：{run?.evidence.status ?? "未记录"}</p><p>{run?.code_dirty == null ? "代码工作区状态未知" : run.code_dirty ? "dirty：仅凭提交无法复现" : "干净工作区"}</p>{run?.failed_gates?.map((gate, gateIndex) => <p className={styles.muted} key={gateIndex}>{gate.metric}：{gate.actual} / {gate.threshold}{!gate.evaluable && " · 不可评估"}</p>)}{run && <Button variant="outline" onClick={() => openRun(run.id)}>查看{String(label)}运行</Button>}</section>;
      })}</div>
      <div className={styles.sideBySide}>{[["修复前冻结门槛", pair.baseline_gates], ["修复后冻结门槛", pair.candidate_gates]].map(([title, gates]) => <details key={String(title)} className={styles.versionDetails}><summary>{String(title)}</summary>{(gates as typeof pair.baseline_gates).map((gate, index) => <p key={index}>{gate.metric}：{gate.actual} / {gate.threshold} · {gate.evaluable ? gate.passed ? "通过" : "未通过" : "不可评估"}</p>)}</details>)}</div>
      {pair.comparison && <><p>{pair.comparison.strict ? "冻结身份具备严格比较条件" : "仅并排证据，不具备严格可比条件"}</p>{pair.comparison.reasons?.map((reason, index) => <p className={styles.notice} key={index}>{reason}</p>)}{pair.comparison.strict && <section aria-label="整批退步项"><h4>整批退步项（含确定性层）</h4>{pair.regressed.length ? pair.regressed.map(key => <p key={key}>{key}</p>) : <p>本次严格对比没有记录退步题。</p>}</section>}</>}
      {pair.baseline_run && pair.candidate_run && <Button variant="outline" onClick={() => navigate({ area: "compare", run: null, case: null, layer: null, panel: null, view: null, va: pair.baseline_run!.id, vb: pair.candidate_run!.id, case_origin: data.id })}>查看完整前后运行对比</Button>}
    </>}</section>}
    {panel === "history" && <section className={styles.turn}><h3>出现与验证历史</h3><p className={styles.muted}>不同断言或内容版本仅在人工审阅明确关联时并列；模型探索不会自动更新处理状态。</p>{data.occurrences.map(occurrence => <div className={styles.datasetSection} key={occurrence.id}><b>{reqV2Time(occurrence.run?.created_at ?? null)}</b><p><State value={occurrence.state} /> · {reqV2LayerLabels[occurrence.layer ?? ""] ?? "整次运行"} · 第{occurrence.repeat || "未记录"}次</p><p>{occurrence.context || "未经人工关联的冻结事实"}</p><p>{occurrence.detail}</p><div className={styles.inlineActions}><Button variant="outline" onClick={() => navigate({ bad_panel: "evidence", bad_occurrence: occurrence.id, bad_repeat: null })}>查看这次证据</Button>{occurrence.run && <Button variant="ghost" onClick={() => openRun(occurrence.run!.id)}>查看运行</Button>}</div><details className={styles.versionDetails}><summary>技术身份与完整性</summary><p>{occurrence.run_name} · {occurrence.layer} / {occurrence.case} · {occurrence.assertion || occurrence.metric}</p><p>断言序号 {occurrence.assertion_index || "未记录"} · 轮次 {occurrence.turn || "未记录"}</p><p>manifest：{occurrence.manifest_sha256 || "见原运行证据"}</p><p>{occurrence.run?.superseded ? "历史归档" : "当前产物"} · {occurrence.run?.evidence.status ?? "引用缺失"}</p></details></div>)}</section>}
  </section>;
}

const buckets = [["open", "待处理"], ["verified", "已验证修复"], ["history", "历史与模型探索"], ["gaps", "证据待补齐"], ["all", "全部分类"]];

export function BadCaseWorkspace({ params, navigate, runs }: { params: URLSearchParams; navigate: Navigate; runs: ReqV2RunSummary[] }) {
  const catalog = useQuery(catalogQuery);
  const id = params.get("bad_id") ?? "";
  const detail = useQuery({ queryKey: ["evaldesk", "reqv2", "bad-case", id], queryFn: () => reqv2.badCase(id), enabled: !!id, retry: false, refetchOnWindowFocus: false });
  const selected = id ? detail : catalog;
  const archived = params.get("bad_archived") === "1";
  const bucket = params.get("bad_bucket") ?? (params.get("bad_status") === "verified" ? "verified" : params.has("bad_run") ? "all" : "open");
  const side = params.get("bad_side") ?? "all", purpose = params.get("bad_purpose") ?? "all", kind = params.get("bad_kind") ?? "all", state = params.get("bad_state") ?? "all";
  const search = (params.get("bad_search") ?? "").toLowerCase();
  const relevantRuns = new Set(groupRunExperiments(runs).filter(group => purpose === "all" || group.purpose === purpose).flatMap(group => group.runs.map(entry => entry.run.id)));
  const byID = new Map((catalog.data?.cases ?? []).map(item => [item.id, item]));
  const matching = new Set((catalog.data?.cases ?? []).filter(item =>
    (side === "all" || item.side === side || item.side === "all") &&
    (kind === "all" || item.kind === kind) && (state === "all" || item.states.includes(state)) &&
    `${item.title} ${item.summary} ${item.occurrences.map(value => value.case ?? "").join(" ")}`.toLowerCase().includes(search) &&
    item.occurrences.some(value => value.run ? (archived || !value.run.superseded) && relevantRuns.has(value.run.id) && (!params.get("bad_run") || value.run.id === params.get("bad_run")) : purpose === "all" && !params.get("bad_run"))
  ).map(item => item.id));
  const groups = (catalog.data?.groups ?? []).filter(group => group.members.some(member => matching.has(member)));
  const visible = groups.filter(group => bucket === "all" || group.category === bucket);
  const pages = Math.max(1, Math.ceil(visible.length / 12)), page = Math.min(pages - 1, Math.max(0, Number(params.get("bad_page")) || 0));
  const change = (updates: Record<string, string | null>) => navigate({ ...updates, bad_page: null });
  const activeGroup = catalog.data?.groups?.find(group => group.id === params.get("bad_group"));
  const listRow = (group: NonNullable<typeof catalog.data>["groups"][number]) => {
    const members = group.members.filter(member => matching.has(member));
    return <button key={group.id} className={styles.badCaseRow} data-testid={`bad-case-group-${group.id}`} onClick={() => navigate({ bad_group: group.id, bad_id: members[0], bad_panel: "overview", bad_occurrence: null, bad_repeat: null })}>
      <span><b>{group.title}</b><small>{sideLabels[group.side]} · {reqV2LayerLabels[group.layer ?? ""] ?? "整次运行"}{group.case && ` · ${group.case}`}</small><small>{group.members.length} 条证据记录 · {group.run_ids.length} 次运行{group.versions > 1 && ` · ${group.versions} 个冻结版本`}</small></span>
      <span>{group.conclusion}<small>{buckets.find(([key]) => key === group.category)?.[1]}</small></span>
      <span>{reqV2Time(group.last_seen)}<small>查看记录与处理依据</small></span>
    </button>;
  };
  return <>
    {params.has("case_return") && <div className={styles.inlineActions}><Button variant="outline" onClick={() => navigate(readReturn(params, "case_return"))}><ArrowLeft size={15} />返回来源工作区</Button></div>}
    {selected.isPending && <p className={styles.empty} role="status">正在读取失败案例证据…</p>}
    {selected.error && <div className={styles.empty} role="alert"><p>{selected.error.message} 案例数量未知。</p><Button variant="outline" onClick={() => void selected.refetch()}>重新读取失败案例</Button></div>}
    {!id && !catalog.error && !catalog.isPending && <>
      {catalog.data?.notes.map((note, index) => <p className={styles.notice} key={index}>{note}</p>)}
      {!!catalog.data?.cases.length && !catalog.data.groups && <p className={styles.notice}>评估服务尚未提供全量整理结果，请重启本机 evaldesk 服务后重新读取。</p>}
      <nav className={styles.filters} aria-label="案例分类">{buckets.map(([key, label]) => <button key={key} aria-current={bucket === key ? "page" : undefined} onClick={() => change({ bad_bucket: key, bad_status: null, bad_verified: null, bad_reference: null })}>{label} <span>{key === "all" ? groups.length : groups.filter(group => group.category === key).length}</span></button>)}</nav>
      <p className={styles.catalogHelp}>默认查看待处理项。同题的断言、重复运行和版本归入一个分类入口；历史结果与根因记录在详情中核对。</p>
      {bucket === "verified" && <p className={styles.scopeNote}>包含同模型严格对照与已关联实现的历史合同验收；每项详情标明验证口径。验收来自冻结批次，不代表今日代码复测或独立保留集通过。</p>}
      <div className={styles.filters} aria-label="案例筛选">
        {[["bad_side", "评估对象", side, [["all", "全部对象"], ["screening", "Screening 初筛"], ["builder", "Builder 选配"]]], ["bad_kind", "问题类型", kind, [["all", "全部类型"], ["question", "题目案例"], ["run", "运行级问题"]]]].map(([name, label, value, options]) => <label className={styles.selectLabel} key={String(name)}><span>{String(label)}</span><select aria-label={String(label)} value={String(value)} onChange={event => change({ [String(name)]: event.target.value })}>{(options as string[][]).map(([key, title]) => <option key={key} value={key}>{title}</option>)}</select></label>)}
        <label className={styles.search}><Search size={15} aria-hidden /><input aria-label="搜索失败案例" value={params.get("bad_search") ?? ""} onChange={event => change({ bad_search: event.target.value || null })} placeholder="搜索场景、表现或题号" /></label>
      </div>
      <details className={styles.versionDetails}><summary>更多筛选与验收范围</summary><div className={styles.filters}>
        <label className={styles.selectLabel}><span>实验目的</span><select aria-label="实验目的" value={purpose} onChange={event => change({ bad_purpose: event.target.value })}><option value="all">全部目的</option>{runPurposes.map(value => <option key={value.key} value={value.key}>{value.label}</option>)}</select></label>
        <label className={styles.selectLabel}><span>历史证据表现</span><select aria-label="历史证据表现" value={state} onChange={event => change({ bad_state: event.target.value })}><option value="all">全部表现</option>{Object.entries(stateLabels).map(([key, label]) => <option key={key} value={key}>{label}</option>)}</select></label>
        <label className={styles.checkbox}><input type="checkbox" checked={archived} onChange={event => change({ bad_archived: event.target.checked ? "1" : null })} />包含归档记录</label>
      </div>{catalog.data?.reference && <section aria-label="验收参考范围"><p>验收参考：{catalog.data.reference.label}</p><p className={styles.muted}>仅代表该冻结批次，未对今日代码重新执行模型评估。参考通过不自动证明历史根因已修复。</p><p><EvidenceReference value={catalog.data.reference.source} /></p></section>}</details>
      {params.get("bad_run") && <Button variant="outline" onClick={() => change({ bad_run: null })}>清除来源运行筛选</Button>}
      {[["question", "题目案例"], ["run", "运行级问题"]].map(([type, label]) => <section aria-label={label} key={type}><div className={styles.catalogHeading}><h2>{label} <span>{visible.filter(group => group.kind === type).length}</span></h2></div>{visible.slice(page * 12, (page + 1) * 12).filter(group => group.kind === type).map(listRow)}</section>)}
      {!visible.length && <p className={styles.empty}>{bucket === "open" ? "当前没有已定位的待处理项；历史与证据缺口可从对应分类查看。" : "当前分类与筛选没有匹配记录。"}</p>}
      {pages > 1 && <div className={styles.pagination}><span>第 {page + 1}/{pages} 页 · {visible.length} 个入口</span><Button variant="outline" disabled={!page} onClick={() => navigate({ bad_page: String(page - 1) })}>上一页</Button><Button variant="outline" disabled={page + 1 === pages} onClick={() => navigate({ bad_page: String(page + 1) })}>下一页</Button></div>}
    </>}
    {id && activeGroup && <section className={styles.turn} aria-label="分组证据目录"><h3>{activeGroup.conclusion}</h3><p>{activeGroup.members.length} 条记录 · {activeGroup.run_ids.length} 次运行。跨版本仅组织为历史目录，各断言与根因分别保留。</p><label className={styles.selectLabel}>分组内证据<select aria-label="分组内证据" value={id} onChange={event => navigate({ bad_id: event.target.value, bad_panel: "overview", bad_occurrence: null, bad_repeat: null, bad_fix: null, bad_pair: null })}>{activeGroup.members.map(member => <option key={member} value={member}>{byID.get(member)?.title} · {byID.get(member)?.occurrences[0]?.assertion || byID.get(member)?.occurrences[0]?.metric || "人工分析"} · {reqV2Time(byID.get(member)?.last_seen ?? null)}</option>)}</select></label></section>}
    {id && detail.data && <CaseInspector data={detail.data} params={params} navigate={navigate} />}
  </>;
}
