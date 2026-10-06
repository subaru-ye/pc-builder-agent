"use client";

// Requirement v2 优先的只读证据工作台:运行目录 → 运行详情(身份/结论/冻结
// 门槛/六层/效率) → 逐题逐轮证据 → 同身份严格对比。工作台不重算分数,
// 结论与门槛全部来自冻结报告;UNEVALUABLE 与 FAIL 用文字区分。
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ArrowLeft, Check, ChevronRight, CircleHelp, RefreshCw, Search, X } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { reqv2, reqV2LayerLabels, reqV2ModeLabels, reqV2Time,
  type ReqV2CaseDetail, type ReqV2CaseIndexEntry, type ReqV2Evidence, type ReqV2GateVerdictRow,
  type ReqV2RunDetail, type ReqV2RunsResponse, type ReqV2RunSummary } from "@/lib/evaldesk/reqv2";
import styles from "./workbench.module.css";

type Navigate = (updates: Record<string, string | null>) => void;

function GateState({ verdict }: { verdict: ReqV2GateVerdictRow }) {
  if (!verdict.evaluable) return <span className={`${styles.state} status-unknown`}><CircleHelp size={14} aria-hidden />UNEVALUABLE · 未满足评估条件</span>;
  return verdict.passed
    ? <span className={`${styles.state} status-pass`}><Check size={14} aria-hidden />PASS</span>
    : <span className={`${styles.state} status-fail`}><X size={14} aria-hidden />FAIL</span>;
}

function EvidenceBadge({ evidence }: { evidence: ReqV2Evidence }) {
  const label = evidence.status === "complete" ? "证据完整" : evidence.status === "incomplete" ? "证据不完整" : "证据无效";
  const tone = evidence.status === "complete" ? "status-pass" : evidence.status === "incomplete" ? "status-review" : "status-fail";
  const Icon = evidence.status === "complete" ? Check : evidence.status === "incomplete" ? CircleHelp : AlertTriangle;
  return <span className={`${styles.state} ${tone}`}><Icon size={14} aria-hidden />{label}</span>;
}

function ErrorMessage({ error, retry }: { error: Error; retry: () => void }) {
  return <div className={styles.empty} role="alert"><p>{error.message}</p><Button variant="outline" onClick={retry}>重新读取</Button></div>;
}

function RunCatalog({ runs, openRun, onReread, pending, error }: {
  runs?: ReqV2RunsResponse; openRun: (id: string) => void; onReread: () => void; pending: boolean; error?: Error;
}) {
  const [type, setType] = useState<string>("all");
  const [evidence, setEvidence] = useState<string>("all");
  const [search, setSearch] = useState("");
  const [showArchived, setShowArchived] = useState(false);
  const all = runs?.runs ?? [];
  const current = all.filter(run => !run.superseded);
  const archived = all.filter(run => run.superseded);
  const query = search.trim().toLowerCase();
  const match = (run: ReqV2RunSummary) =>
    (type === "all" || (type === "zero_model" ? run.zero_model : run.mode === type)) &&
    (evidence === "all" || run.evidence.status === evidence) &&
    `${run.label} ${run.grader_version} ${run.models.map(m => m.model).join(" ")}`.toLowerCase().includes(query);
  const visible = current.filter(match);
  const visibleArchived = archived.filter(match);
  const row = (run: ReqV2RunSummary) => <button key={run.id} className={styles.catalogRow} data-testid={`reqv2-run-${run.dir_name}`} onClick={() => openRun(run.id)}>
    <span className={styles.catalogIdentity}><strong>{run.dir_name}</strong><time dateTime={run.created_at ?? undefined}>{reqV2Time(run.created_at)}</time></span>
    <span className={styles.catalogContext}><b>{reqV2ModeLabels[run.mode] ?? run.mode}</b><small>{run.models.map(m => m.model).join(" · ") || "模型未记录"} · {run.splits.join("/") || "split 未记录"} · {run.repeats} 次</small></span>
    <span className={run.gate_passed == null ? "status-unknown" : run.gate_passed ? "status-pass" : "status-fail"}>{run.gate_passed == null ? "未记录" : run.gate_passed ? "通过" : "未通过"}</span>
    <EvidenceBadge evidence={run.evidence} />
    <ChevronRight size={16} className={styles.catalogArrow} aria-hidden />
  </button>;
  return <section aria-label="Requirement v2 运行目录">
    <div className={styles.catalogSummary} aria-label="运行概况">
      <div><strong>{current.length}</strong><span>当前运行</span></div>
      <div><strong>{current.filter(run => run.evidence.status === "complete").length}</strong><span>证据完整</span></div>
      <div><strong>{current.filter(run => run.gate_passed === true).length}</strong><span>门槛通过</span></div>
      <div><strong>{archived.length}</strong><span>历史归档</span></div>
    </div>
    <p className={styles.catalogHelp}>门槛是冻结评估结论；证据状态只表示产物能否核对，不代表方案通过。</p>
    <div className={styles.catalogToolbar}>
      <div className={styles.catalogTabs} aria-label="运行类型">
        {["all", "live", "deterministic", "replay", "regrade"].map(value => <button key={value} aria-pressed={type === value} onClick={() => setType(value)}>{value === "all" ? "全部" : reqV2ModeLabels[value]}</button>)}
      </div>
      <div className={styles.catalogTools}>
        <label className={styles.catalogSelect}><span className="sr-only">证据状态</span><select value={evidence} onChange={event => setEvidence(event.target.value)}><option value="all">全部证据</option><option value="complete">证据完整</option><option value="incomplete">证据不完整</option><option value="invalid">证据无效</option></select></label>
        <label className={styles.search}><Search size={16} aria-hidden /><span className="sr-only">搜索运行</span><input value={search} onChange={event => setSearch(event.target.value)} placeholder="搜索运行或模型" /></label>
      </div>
    </div>
    {pending && <p className={styles.empty} role="status">正在扫描 artifacts/reqv2…</p>}
    {error && <ErrorMessage error={error} retry={onReread} />}
    {runs && !pending && !error && <>
      <div className={styles.catalogHeading}><h2>当前运行 <span>{visible.length}</span></h2><span>按创建时间排列</span></div>
      <div className={styles.catalogColumns} aria-hidden><span>运行 / 时间</span><span>类型 / 模型 / 范围</span><span>冻结门槛</span><span>证据</span><span /></div>
      {visible.map(row)}
      {!visible.length && <div className={styles.empty}>{all.length ? "当前运行没有匹配项，请调整筛选条件。" : "artifacts/reqv2 下尚无 Requirement v2 运行。"}</div>}
      {archived.length > 0 && <details className={styles.versionDetails} open={showArchived} onToggle={e => setShowArchived(e.currentTarget.open)}>
        <summary>superseded 历史归档 · {archived.length} 次（不作为当前基线）</summary>
        {visibleArchived.map(row)}
        {!visibleArchived.length && <p className={styles.empty}>归档中没有匹配项。</p>}
      </details>}
    </>}
  </section>;
}

const gateGroups: Array<{ key: string; title: string; hint: string; layers: string[] }> = [
  { key: "deterministic", title: "确定性层门槛", hint: "reducer / readiness / policy / ui-contract,程序判定,要求全部通过", layers: ["reducer", "readiness", "policy", "ui-contract"] },
  { key: "model", title: "模型层门槛", hint: "初筛抽取与多轮对话的冻结阈值", layers: ["extraction", "conversations", "model"] },
];

function RunDetail({ run, openCase, openCompare, back }: { run: ReqV2RunDetail; openCase: (layer: string, id: string) => void; openCompare: () => void; back: () => void }) {
  const [layerFilter, setLayerFilter] = useState<string>("failed");
  const zeroModel = run.zero_model;
  const identity: Array<[string, string]> = [
    ["产物目录", run.label], ["运行类型", `${reqV2ModeLabels[run.mode] ?? run.mode}${run.regrade ? "(复用源运行冻结观测)" : ""}`],
    ["创建时间", reqV2Time(run.created_at)], ["splits", run.splits.join(" / ") || "未记录"], ["repeats", String(run.repeats)],
    ["判卷版本", run.grader_version], ["manifest 冻结哈希", run.manifest_sha256 || "未记录"], ["gates 冻结哈希", run.gates_sha256 || "未记录"],
    ["代码提交", `${run.code_commit || "未记录"}${run.code_dirty == null ? "" : run.code_dirty ? "(工作区有未提交修改)" : "(干净)"}`],
    ["调用预算", run.max_model_requests ? `${run.max_model_requests} 次` : "未声明"],
    ["模型", run.models.map(m => `${m.role}:${m.model}`).join(" · ") || "未配置(零模型)"],
  ];
  if (run.regrade_plan) identity.push(["重判来源", JSON.stringify(run.regrade_plan)]);
  const conclusion = run.gate_passed == null ? "结论未记录" : run.gate_passed ? "冻结门槛全部通过" : "冻结门槛未全过:候选不可发布";
  const gateGroup = (group: typeof gateGroups[number]) => {
    const rows = run.gate_verdicts.filter(v => group.layers.includes(v.layer) || (group.key === "model" && v.layer === "global"));
    if (!rows.length) return null;
    return <section className={styles.turn} key={group.key}><h3>{group.title}</h3><p className={styles.muted}>{group.hint}</p>
      <div>{rows.map((verdict, i) => <div key={i} className={`${styles.caseGrid} ${styles.caseRow}`} data-testid={`gate-${verdict.layer}-${verdict.metric}`}>
        <span><b>{reqV2LayerLabels[verdict.layer] ?? verdict.layer}</b><small className={styles.block}>{verdict.metric}{verdict.note ? ` · ${verdict.note}` : ""}</small></span>
        <span><span className={styles.mobileLabel}>实际</span>{verdict.actual || "—"}</span>
        <span><span className={styles.mobileLabel}>阈值</span>{verdict.threshold || "—"}</span>
        <GateState verdict={verdict} />
      </div>)}</div>
    </section>;
  };
  const layerRow = (layer: string) => {
    const row = run.per_layer[layer];
    if (!row) return null;
    const quality = run.model_quality[layer] as Record<string, unknown> | undefined;
    const classifications = Object.entries(row.failure_classifications ?? {});
    return <div key={layer} className={`${styles.caseGrid} ${styles.caseRow}`} data-testid={`layer-${layer}`}>
      <span><b>{reqV2LayerLabels[layer] ?? layer}</b><small className={styles.block}>{classifications.map(([key, count]) => `${key} ×${count}`).join(" · ") || "无失败分类"}</small></span>
      <span><span className={styles.mobileLabel}>Pass^k</span>{row.passed}/{row.cases}<small className={styles.block}>跳过 {row.skipped} · veto {row.vetoes}</small></span>
      {quality != null && <span className={styles.muted}><details><summary>模型层指标(不与确定性层平均)</summary><pre className="whitespace-pre-wrap break-all text-xs">{JSON.stringify(quality, null, 1)}</pre></details></span>}
    </div>;
  };
  const failedFirst = (a: ReqV2CaseIndexEntry, b: ReqV2CaseIndexEntry) =>
    Number(a.pass_k) - Number(b.pass_k) || a.layer.localeCompare(b.layer) || a.id.localeCompare(b.id);
  const caseRows = run.cases
    .filter(entry => layerFilter === "failed" ? !entry.pass_k : layerFilter === "all" || entry.layer === layerFilter)
    .sort(failedFirst);
  const usage = run.usage as Record<string, unknown> | undefined;
  return <section aria-label="v2 运行详情">
    <div className={styles.sectionHead}><div><h2>{run.dir_name}</h2><p>{reqV2ModeLabels[run.mode] ?? run.mode} · {reqV2Time(run.created_at)}</p></div>
      <Button variant="outline" onClick={back}><ArrowLeft size={15} />返回运行目录</Button></div>
    <div className={styles.detailOverview} aria-label="运行状态">
      <div data-testid="reqv2-conclusion"><span>冻结门槛</span><strong className={run.gate_passed ? "status-pass" : run.gate_passed == null ? "status-unknown" : "status-fail"}>{conclusion}</strong></div>
      <div><span>证据状态</span><strong><EvidenceBadge evidence={run.evidence} /></strong></div>
      <div><span>运行范围</span><strong>{run.splits.join("/") || "split 未记录"} · {run.repeats} 次重复</strong></div>
    </div>
    {run.evidence.notes.length > 0 && <div className={run.evidence.status === "complete" ? styles.detailNotes : styles.notice} role={run.evidence.status === "complete" ? undefined : "alert"}>{run.evidence.notes.map((note, i) => <p key={i}>{note}</p>)}</div>}
    <section className={styles.turn} aria-label="报告结论"><h3>报告结论</h3>
      <p className={styles.muted}>{run.conclusion || "报告未写结论。"}</p>
      {run.limitations.length > 0 && <details className={styles.versionDetails} open><summary>报告声明的限制({run.limitations.length} 条)</summary>{run.limitations.map((item, i) => <p key={i}>· {item}</p>)}</details>}
    </section>
    <details className={styles.detailTechnical}><summary>运行身份与完整性核对 · {run.integrity_checks.length} 项</summary>
      <dl className={styles.detailIdentity}>{identity.map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl>
      <p className={styles.muted}>核对的是证据完整性，不代表冻结门槛通过。</p>
      {run.integrity_checks.map((check, i) => <p key={i}>{check.state === "ok" ? "✓" : "×"} {check.check}{check.detail ? ` — ${check.detail}` : ""}</p>)}
    </details>
    <section className={styles.turn} aria-label="冻结门槛"><h3>冻结门槛({run.gate_verdicts.length} 项,来自冻结报告,不可在工作台修改)</h3>
      {gateGroups[0] && gateGroup(gateGroups[0])}{gateGroup(gateGroups[1])}
    </section>
    <section className={styles.turn} aria-label="六层结果"><h3>六层结果</h3>
      <p className={styles.muted}>确定性层要求 100%;模型层单独显示 precision/recall/任务成功,不与确定性层平均。</p>
      <div>
        {["reducer", "readiness", "policy", "ui-contract"].map(layerRow)}
        {["extraction", "conversations"].map(layerRow)}
      </div>
    </section>
    <section className={styles.turn} aria-label="效率与限制"><h3>调用与用量</h3>
      {zeroModel
        ? <p className={styles.muted}>零模型运行:模型调用、provider 成功率与延迟不适用,不在此显示。</p>
        : usage != null
          ? <details className={styles.versionDetails} open><summary>模型调用与 token</summary><pre className="whitespace-pre-wrap break-all text-xs">{JSON.stringify(usage, null, 1)}</pre></details>
          : <p className={styles.muted}>用量未记录。</p>}
    </section>
    <section className={styles.turn} aria-label="逐题索引"><h3>逐题证据({run.cases.length} 题)</h3>
      <div className={styles.filters} aria-label="题目过滤">
        <button aria-pressed={layerFilter === "failed"} onClick={() => setLayerFilter("failed")}>未通过 Pass^k 优先</button>
        <button aria-pressed={layerFilter === "all"} onClick={() => setLayerFilter("all")}>全部层</button>
        {Object.keys(run.per_layer).map(layer => <button key={layer} aria-pressed={layerFilter === layer} onClick={() => setLayerFilter(layer)}>{reqV2LayerLabels[layer] ?? layer}</button>)}
      </div>
      <div>{caseRows.map(entry => <button key={`${entry.layer}/${entry.id}`} className={`${styles.caseGrid} ${styles.caseRow}`} onClick={() => openCase(entry.layer, entry.id)}>
        <span><b>{entry.id}</b><small className={styles.block}>{reqV2LayerLabels[entry.layer] ?? entry.layer} · {entry.split}</small></span>
        <span><span className={styles.mobileLabel}>repeats</span>{entry.repeats.join(", ")}</span>
        <span>{entry.skipped ? <span className="status-unknown">已跳过 · 未评估</span> : entry.pass_k ? <span className="status-pass">Pass^k 通过</span> : <span className="status-fail">Pass^k 未通过</span>}{entry.vetoes > 0 && <small className="status-fail block">veto ×{entry.vetoes}</small>}</span>
        <ChevronRight className={styles.rowChevron} size={16} aria-hidden />
      </button>)}
      {!caseRows.length && <div className={styles.empty}>此过滤没有题目。</div>}</div>
    </section>
    <section className={styles.turn} aria-label="对比"><h3>对比</h3>
      <p className={styles.muted}>严格对比只允许同一冻结身份(判卷/manifest/gates/split/repeats 一致)的同语义运行。</p>
      <Button variant="outline" onClick={openCompare}>选择两次运行对比<ChevronRight size={14} /></Button>
    </section>
  </section>;
}

function CaseDetail({ data, back }: { data: NonNullable<ReturnType<typeof useQuery<ReqV2CaseDetail>>["data"]>; back: () => void }) {
  const failing = data.repeats.find(repeat => !repeat.pass) ?? data.repeats[0];
  const [selected, setSelected] = useState<number | null>(null);
  const repeat = data.repeats.find(r => r.repeat === selected) ?? failing;
  return <section aria-label="v2 逐题证据" className={styles.inspector}>
    <div className={styles.sectionHead}><div><h2>{data.layer} / {data.case}</h2><p>{data.split} · session {data.session || "未记录"} · {data.repeats.every(item => item.skipped) ? "已跳过 · 未评估" : `Pass^k ${data.pass_k ? "通过" : "未通过"}`}</p></div>
      <Button variant="outline" onClick={back}><ArrowLeft size={15} />返回运行</Button></div>
    {data.integrity.status !== "complete" && <p className={styles.notice}>本运行证据{data.integrity.status === "invalid" ? "无效" : "不完整"}:{data.integrity.notes.join(";")}</p>}
    <div className={styles.filters} aria-label="重复次数">{data.repeats.map(item => <button key={item.repeat} aria-pressed={item.repeat === repeat.repeat} onClick={() => setSelected(item.repeat)}>第 {item.repeat} 次{item.pass ? "" : " · 失败"}</button>)}</div>
    <section className={styles.turn}><h3>第 {repeat.repeat} 次重复{repeat.skipped ? ` · 已跳过(${repeat.skipped})` : ""}</h3>
      {repeat.error && <p role="alert" className="status-fail">{repeat.error}</p>}
      {!!repeat.vetoes?.length && <p className="status-fail">veto:{repeat.vetoes.join(";")}</p>}
      <h4>断言</h4>
      <div>{repeat.assertions.map((assertion, i) => <div key={i} className={`${styles.caseGrid} ${styles.caseRow}`}>
        <span>{assertion.pass ? <Check size={14} className="status-pass inline" aria-hidden /> : <X size={14} className="status-fail inline" aria-hidden />} <b>{assertion.name}</b></span>
        <span className={styles.muted}>{assertion.classification || ""}{assertion.detail ? `${assertion.classification ? " · " : ""}${assertion.detail}` : ""}</span>
      </div>)}</div>
      {!!repeat.turns?.length && <><h4>逐轮实际观测</h4>{repeat.turns.map(turn => <details key={turn.index} className={styles.versionDetails} open={turn.index === 1}><summary>第 {turn.index} 轮{turn.reply ? "" : " · 无可见回复"}</summary>
        {!!turn.operations?.length && <><h4 className={styles.mobileLabel}>实际操作</h4><ul className="text-xs">{turn.operations.map((op, i) => <li key={i}><code className="break-all">{op}</code></li>)}</ul></>}
        {turn.reply && <p className={styles.muted}>回复:{turn.reply}</p>}
        {!!turn.turn_signals?.length && <p className={styles.muted}>turn signals:{turn.turn_signals.join("、")}</p>}
        {turn.screen_model_called != null && <p className={styles.muted}>screening 模型调用:{turn.screen_model_called ? "是" : "否"}</p>}
      </details>)}</>}
      {repeat.observation_rest != null && <details className={styles.versionDetails}><summary>readiness / 最终状态 / UI 观测(原文)</summary><pre className="whitespace-pre-wrap break-all text-xs">{JSON.stringify(repeat.observation_rest, null, 1)}</pre></details>}
    </section>
    <section className={styles.turn}><h3>冻结题目(本运行内,哈希已核对)</h3>
      {data.frozen_note && <p className={styles.notice}>{data.frozen_note}</p>}
      {data.frozen && <>
        {data.frozen.title && <p><b>{data.frozen.title}</b></p>}
        {data.frozen.rationale && <p className={styles.muted}>{data.frozen.rationale}</p>}
        <details className={styles.versionDetails} open><summary>冻结输入与期望</summary><pre className="whitespace-pre-wrap break-all text-xs">{JSON.stringify(data.frozen.fields, null, 1)}</pre></details>
      </>}
    </section>
  </section>;
}

function CompareView({ params, runs, back }: { params: URLSearchParams; runs: ReqV2RunSummary[]; back: () => void }) {
  const a = params.get("va") ?? "", b = params.get("vb") ?? "";
  const compare = useQuery({ queryKey: ["evaldesk", "reqv2", "compare", a, b], queryFn: () => reqv2.compare(a, b), enabled: !!a && !!b, retry: false, refetchOnWindowFocus: false });
  const select = (role: "va" | "vb", id: string) => {
    const next = new URLSearchParams(window.location.search);
    next.set(role, id);
    window.history.replaceState(null, "", `/eval?${next}`);
  };
  const byId = (id: string) => runs.find(run => run.id === id);
  return <section aria-label="v2 运行对比">
    <div className={styles.sectionHead}><div><h2>同身份对比</h2><p>严格对比要求判卷/manifest/gates/split/repeats 与运行语义一致;否则只并列查看。</p></div>
      <Button variant="outline" onClick={back}><ArrowLeft size={15} />返回运行目录</Button></div>
    <div className={styles.selection}>
      {[["va", "基线"], ["vb", "候选"]].map(([role, label]) => <label key={role} className={styles.selectLabel}><span>{label}运行</span>
        <select aria-label={`${label}运行`} value={role === "va" ? a : b} onChange={e => select(role as "va" | "vb", e.target.value)}><option value="">选择运行</option>{runs.map(run => <option key={run.id} value={run.id}>{`${reqV2Time(run.created_at)} · ${run.dir_name}`}</option>)}</select></label>)}
    </div>
    {compare.isPending && <p className={styles.empty} role="status">正在核对两次运行的冻结身份…</p>}
    {compare.error && <ErrorMessage error={compare.error} retry={() => void compare.refetch()} />}
    {compare.data && <>
      {!compare.data.strict && <div className={styles.notice} role="alert" data-testid="compare-incomparable"><b>不具备严格比较条件;以下只并列证据,不给出改进/退步幅度。</b><ul>{compare.data.reasons?.map((reason, i) => <li key={i}>{reason}</li>)}</ul></div>}
      {compare.data.strict && compare.data.outcome && <div data-testid="compare-outcome">
        <p>共同题 {compare.data.case_set.common} · 模型层配对 {compare.data.outcome.model_pairs}</p>
        <p>持续通过 {compare.data.outcome.still_pass} · 持续失败 {compare.data.outcome.still_fail} · 退步 {compare.data.outcome.regressed.length} · 改善 {compare.data.outcome.newly_passing.length}</p>
        {compare.data.outcome.mcnemar_p != null && <p>McNemar 精确检验 p = {compare.data.outcome.mcnemar_p}</p>}
        {compare.data.outcome.sample_note && <p className={styles.notice}>{compare.data.outcome.sample_note}</p>}
        {compare.data.outcome.mcnemar_note && <p className={styles.muted}>{compare.data.outcome.mcnemar_note}</p>}
        {Object.entries(compare.data.outcome.deterministic_layers).map(([layer, delta]) => <p key={layer} className={styles.muted}>确定性层 {reqV2LayerLabels[layer] ?? layer}:{delta.baseline_pass}/{delta.total} → {delta.candidate_pass}/{delta.total}(不进入模型统计)</p>)}
        {!!compare.data.outcome.regressed.length && <details open className={styles.versionDetails}><summary>退步题目</summary>{compare.data.outcome.regressed.map(key => <p key={key}>{key}</p>)}</details>}
        {!!compare.data.outcome.newly_passing.length && <details className={styles.versionDetails}><summary>新通过题目</summary>{compare.data.outcome.newly_passing.map(key => <p key={key}>{key}</p>)}</details>}
      </div>}
      <div className={styles.sideBySide}>
        {[compare.data.baseline, compare.data.candidate].map(run => <div key={run.id}><h3>{run.dir_name}</h3>
          <p>{reqV2ModeLabels[run.mode] ?? run.mode} · {reqV2Time(run.created_at)}</p>
          <p>{run.gate_passed == null ? "结论未记录" : run.gate_passed ? "冻结门槛通过" : "冻结门槛未全过"}</p>
          <p className={styles.muted}>{run.grader_version} · manifest {run.manifest_sha256 || "未记录"}</p>
        </div>)}
      </div>
      {!a || !b ? <p className={styles.empty}>请选择两次运行。</p> : null}
      {runs.length > 0 && <p className={styles.muted}>可选运行 {runs.length} 次{byId(a)?.superseded || byId(b)?.superseded ? ";归档运行只作历史证据,不作为现行基线。" : "."}</p>}
    </>}
  </section>;
}

export function ReqV2Workbench({ params, navigate, onReread }: { params: URLSearchParams; navigate: Navigate; onReread: () => void }) {
  const queryClient = useQueryClient();
  const runs = useQuery({ queryKey: ["evaldesk", "reqv2", "runs"], queryFn: reqv2.runs, retry: false, staleTime: 30_000, refetchOnWindowFocus: false });
  const runId = params.get("run") ?? "";
  const layer = params.get("layer") ?? "";
  const caseId = params.get("case") ?? "";
  const comparing = params.get("view") === "compare";
  const run = useQuery({ queryKey: ["evaldesk", "reqv2", "run", runId], queryFn: () => reqv2.run(runId), enabled: !!runId, retry: false, refetchOnWindowFocus: false });
  const caseDetail = useQuery({ queryKey: ["evaldesk", "reqv2", "case", runId, layer, caseId], queryFn: () => reqv2.case(runId, layer, caseId), enabled: !!runId && !!layer && !!caseId, retry: false, refetchOnWindowFocus: false });
  const reread = () => {
    void queryClient.invalidateQueries({ queryKey: ["evaldesk", "reqv2"] });
    onReread();
  };
  return <main className={styles.content}><div className={styles.titleRow}><div><h1 id="eval-page-heading" tabIndex={-1}>Requirement v2 评估工作台</h1><p>本机冻结运行 · 只读审阅</p></div>
    <Button variant="outline" onClick={reread}><RefreshCw size={15} />重新读取</Button></div>
    {runs.data?.warnings.map((warning, i) => <p key={i} className={styles.notice}>{warning}</p>)}
    {runId && !comparing && run.isPending && <p className={styles.empty} role="status">正在读取运行详情…</p>}
    {runId && !comparing && run.error && <ErrorMessage error={run.error} retry={() => void run.refetch()} />}
    {runId && run.data && !caseId && !comparing && <RunDetail run={run.data} back={() => navigate({ run: null })} openCase={(caseLayer, id) => navigate({ layer: caseLayer, case: id })} openCompare={() => navigate({ desk: "reqv2", view: "compare" })} />}
    {runId && layer && caseId && !comparing && (caseDetail.isPending
      ? <p className={styles.empty} role="status">正在读取逐题证据…</p>
      : caseDetail.error
        ? <ErrorMessage error={caseDetail.error} retry={() => void caseDetail.refetch()} />
        : caseDetail.data && <CaseDetail data={caseDetail.data} back={() => navigate({ case: null, layer: null })} />)}
    {comparing && runs.data && <CompareView params={params} runs={runs.data.runs} back={() => navigate({ view: null })} />}
    {!runId && !comparing && <RunCatalog runs={runs.data} pending={runs.isPending} error={runs.error ?? undefined} openRun={id => navigate({ run: id })} onReread={reread} />}
  </main>;
}
