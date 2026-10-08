"use client";

// Requirement v2 只读工作区:角色总览 / 迭代资产 / 测试记录 → 分区详情
// → 逐题逐轮证据 → 同身份严格对比。工作台不重算分数,
// 结论与门槛全部来自冻结报告;UNEVALUABLE 与 FAIL 用文字区分。分层按
// 初筛 Screening 侧 / 选配 Builder 侧组织(见 lib/evaldesk/reqv2.ts)。
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ArrowLeft, Check, ChevronRight, CircleHelp, RefreshCw, Search, X } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { reqv2, reqV2LayerLabels, reqV2ModeLabels, reqV2Sides, reqV2Time,
  type ReqV2CaseDetail, type ReqV2CaseIndexEntry, type ReqV2CompareOutcome, type ReqV2Evidence, type ReqV2GateVerdictRow,
  type ReqV2RunDetail, type ReqV2RunsResponse, type ReqV2RunSummary } from "@/lib/evaldesk/reqv2";
import styles from "./workbench.module.css";
import { PromptWorkspace, SideWorkspace, WorkspaceNav, workspaceSections, type WorkspaceSection } from "./sections";
import { DatasetCoverage, DatasetWorkspace } from "./datasets";

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
  const [gate, setGate] = useState("all");
  const [page, setPage] = useState(0);
  const all = runs?.runs ?? [];
  const current = all.filter(run => !run.superseded);
  const archived = all.filter(run => run.superseded);
  const query = search.trim().toLowerCase();
  const match = (run: ReqV2RunSummary) =>
    (type === "all" || (type === "zero_model" ? run.zero_model : run.mode === type)) &&
    (evidence === "all" || run.evidence.status === evidence) &&
    (gate === "all" || (gate === "pass" ? run.gate_passed === true : gate === "fail" ? run.gate_passed === false : run.gate_passed == null)) &&
    `${run.label} ${run.grader_version} ${run.models.map(m => m.model).join(" ")}`.toLowerCase().includes(query);
  const visible = current.filter(match);
  const visibleArchived = archived.filter(match);
  const pageCount = Math.max(1, Math.ceil(visible.length / 12));
  const currentPage = Math.min(page, pageCount - 1);
  const row = (run: ReqV2RunSummary) => <button key={run.id} className={styles.catalogRow} data-testid={`reqv2-run-${run.dir_name}`} onClick={() => openRun(run.id)}>
    <span className={styles.catalogIdentity}><strong>{run.dir_name}</strong><time dateTime={run.created_at ?? undefined}>{reqV2Time(run.created_at)}</time></span>
    <span className={styles.catalogContext}><b>{reqV2ModeLabels[run.mode] ?? run.mode}</b><small>{run.models.map(m => m.model).join(" · ") || "模型未记录"} · {run.splits.join("/") || "split 未记录"} · {run.repeats} 次</small>{run.score_note && <small>{run.score_note}</small>}</span>
    <span className={run.gate_passed == null ? "status-unknown" : run.gate_passed ? "status-pass" : "status-fail"}>{run.gate_passed == null ? "未记录" : run.gate_passed ? "通过" : "未通过"}</span>
    <EvidenceBadge evidence={run.evidence} />
    <ChevronRight size={16} className={styles.catalogArrow} aria-hidden />
  </button>;
  return <section aria-label="运行目录">
    <div className={styles.catalogSummary} aria-label="运行概况">
      <div><strong>{current.length}</strong><span>当前运行</span></div>
      <div><strong>{current.filter(run => run.evidence.status === "complete").length}</strong><span>证据完整</span></div>
      <div><strong>{current.filter(run => run.gate_passed === true).length}</strong><span>门槛通过</span></div>
      <div><strong>{archived.length}</strong><span>历史归档</span></div>
    </div>
    <p className={styles.catalogHelp}>门槛是冻结评估结论；证据状态只表示产物能否核对，不代表方案通过。</p>
    <div className={styles.catalogToolbar} onChange={() => setPage(0)}>
      <div className={styles.catalogTabs} aria-label="运行类型">
        {["all", "live", "deterministic", "replay", "regrade"].map(value => <button key={value} aria-pressed={type === value} onClick={() => { setType(value); setPage(0); }}>{value === "all" ? "全部" : reqV2ModeLabels[value]}</button>)}
      </div>
      <div className={styles.catalogTools}>
        <label className={styles.catalogSelect}><span className="sr-only">冻结门槛筛选</span><select value={gate} onChange={e => setGate(e.target.value)}><option value="all">全部门槛</option><option value="fail">未通过</option><option value="pass">通过</option><option value="unknown">未记录</option></select></label>
        <label className={styles.catalogSelect}><span className="sr-only">证据状态</span><select value={evidence} onChange={event => setEvidence(event.target.value)}><option value="all">全部证据</option><option value="complete">证据完整</option><option value="incomplete">证据不完整</option><option value="invalid">证据无效</option></select></label>
        <label className={styles.search}><Search size={16} aria-hidden /><span className="sr-only">搜索运行</span><input value={search} onChange={event => setSearch(event.target.value)} placeholder="搜索运行或模型" /></label>
      </div>
    </div>
    {pending && <p className={styles.empty} role="status">正在扫描 artifacts/reqv2…</p>}
    {error && <ErrorMessage error={error} retry={onReread} />}
    {runs && !pending && !error && <>
      <div className={styles.catalogHeading}><h2>当前运行 <span>{visible.length}</span></h2><span>按创建时间排列</span></div>
      <div className={styles.catalogColumns} aria-hidden><span>运行 / 时间</span><span>类型 / 模型 / 范围</span><span>冻结门槛</span><span>证据</span><span /></div>
      {visible.slice(currentPage * 12, (currentPage + 1) * 12).map(row)}
      {pageCount > 1 && <div className={styles.pagination}><span>第 {currentPage + 1} / {pageCount} 页 · {visible.length} 次运行</span><Button variant="outline" disabled={currentPage === 0} onClick={() => setPage(currentPage - 1)}>上一页</Button><Button variant="outline" disabled={currentPage + 1 === pageCount} onClick={() => setPage(currentPage + 1)}>下一页</Button></div>}
      {!visible.length && <div className={styles.empty}>{all.length ? "当前运行没有匹配项，请调整筛选条件。" : "尚无评估运行，请先生成评估产物后重新读取。"}</div>}
      {archived.length > 0 && <details className={styles.versionDetails} open={showArchived} onToggle={e => setShowArchived(e.currentTarget.open)}>
        <summary>superseded 历史归档 · {archived.length} 次（不作为当前基线）</summary>
        {visibleArchived.map(row)}
        {!visibleArchived.length && <p className={styles.empty}>归档中没有匹配项。</p>}
      </details>}
    </>}
  </section>;
}

const gateSideGroups: Array<{ key: string; title: string; hint: string; layers: string[] }> = [
  { key: "screening", title: "初筛侧门槛", hint: "真实 Screening 模型层的冻结阈值(初筛抽取 / 多轮对话 / model 全局统计)", layers: ["extraction", "conversations", "model"] },
  { key: "cross", title: "跨层门槛", hint: "覆盖全部层的 veto 总数与跨模型层的关键字段误写", layers: ["all", "extraction+conversations"] },
];

// 模型层指标按已知键结构化展示;未知形状整体省略,原始值在 report.json。
function ModelMetrics({ quality }: { quality: Record<string, unknown> }) {
  const n = (v: unknown) => typeof v === "number" ? v : null;
  const parts: string[] = [];
  const rate = (key: string, label: string) => {
    const value = n(quality[key]);
    if (value != null) parts.push(`${label} ${value.toFixed(3)}`);
  };
  rate("op_precision", "操作 precision");
  rate("op_recall", "召回");
  const taskP = n(quality.task_passed), taskT = n(quality.task_total);
  if (taskP != null && taskT != null) parts.push(`任务成功 ${taskP}/${taskT}`);
  const sigM = n(quality.signal_matches), sigT = n(quality.signal_turns);
  if (sigM != null && sigT != null) parts.push(`signal ${sigM}/${sigT}`);
  for (const [key, label] of [["forbidden_op_failures", "禁用操作"], ["repeated_questions", "重复追问"], ["key_field_wrong_writes", "关键字段误写"]] as const) {
    const value = n(quality[key]);
    if (value != null) parts.push(`${label} ${value}`);
  }
  if (!parts.length) return null;
  return <span className={styles.muted}><span className={styles.mobileLabel}>模型层指标</span>{parts.join(" · ")}</span>;
}

function RunDetail({ run, openCase, openCompare, back, backLabel, panel, setPanel, side }: { run: ReqV2RunDetail; openCase: (layer: string, id: string) => void; openCompare: () => void; back: () => void; backLabel: string; panel: string; setPanel: (panel: string) => void; side?: string }) {
  const [layerFilter, setLayerFilter] = useState<string>("failed");
  const [caseSearch, setCaseSearch] = useState("");
  const sideLayers = reqV2Sides.find(item => item.key === side)?.layers;
  const panels = [["results", "结果概览"], ["gates", "门槛检查"], ["dataset", "评估集与题目"], ["prompts", "模型与提示词"], ["evidence", "运行证据"]];
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
  const gateGroup = (group: { key: string; title: string; hint: string; rows: ReqV2GateVerdictRow[] }) => {
    if (!group.rows.length) return null;
    return <section className={styles.turn} key={group.key}><h3>{group.title}</h3><p className={styles.muted}>{group.hint}</p>
      <div>{group.rows.map((verdict, i) => <div key={i} className={`${styles.caseGrid} ${styles.caseRow}`} data-testid={`gate-${verdict.layer}-${verdict.metric}`}>
        <span><b>{reqV2LayerLabels[verdict.layer] ?? verdict.layer}</b><small className={styles.block}>{verdict.metric}{verdict.note ? ` · ${verdict.note}` : ""}</small></span>
        <span><span className={styles.mobileLabel}>实际</span>{verdict.actual || "—"}</span>
        <span><span className={styles.mobileLabel}>阈值</span>{verdict.threshold || "—"}</span>
        <GateState verdict={verdict} />
      </div>)}</div>
    </section>;
  };
  // 分组后剩余的 verdict 原样显示,不再静默丢弃。
  const groupedGateLayers = new Set(gateSideGroups.flatMap(group => group.layers));
  const gateGroups = [
    ...gateSideGroups.map(group => ({ key: group.key, title: group.title, hint: group.hint, rows: run.gate_verdicts.filter(v => group.layers.includes(v.layer)) })),
    { key: "rest", title: "其他门槛", hint: "层归属未知的冻结阈值,按原样列出", rows: run.gate_verdicts.filter(v => !groupedGateLayers.has(v.layer)) },
  ];
  const layerScoreRow = (layer: string) => {
    const row = run.per_layer[layer];
    if (!row) return null;
    const quality = run.model_quality[layer] as Record<string, unknown> | undefined;
    const classifications = Object.entries(row.failure_classifications ?? {});
    const empty = row.cases === 0;
    return <div key={layer} className={`${styles.caseGrid} ${styles.caseRow}`} data-testid={`layer-${layer}`}>
      <span><b>{reqV2LayerLabels[layer] ?? layer}</b>{(!side || classifications.length > 0) && <small className={styles.block}>{classifications.map(([key, count]) => `${key} ×${count}`).join(" · ") || (empty ? "未评估" : "无失败分类")}</small>}</span>
      <span><span className={styles.mobileLabel}>Pass^k</span>{empty ? <span className="status-unknown">跳过 · 未评估</span> : <>{row.passed}/{row.cases}{(!side || row.skipped > 0 || row.vetoes > 0) && <small className={styles.block}>跳过 {row.skipped} · veto {row.vetoes}</small>}</>}</span>
      {quality != null && <ModelMetrics quality={quality} />}
    </div>;
  };
  // 逐题索引按两侧排序:初筛层在前,选配层在后,未知层垫底。
  const layerOrder = [...reqV2Sides.flatMap(side => side.layers), ...Object.keys(run.per_layer).filter(layer => !reqV2Sides.some(side => side.layers.includes(layer)))];
  const failedFirst = (a: ReqV2CaseIndexEntry, b: ReqV2CaseIndexEntry) =>
    Number(a.pass_k) - Number(b.pass_k) || layerOrder.indexOf(a.layer) - layerOrder.indexOf(b.layer) || a.id.localeCompare(b.id);
  const effectiveFilter = panel === "dataset" && layerFilter === "failed" ? "all" : layerFilter;
  const caseRows = run.cases
    .filter(entry => (!sideLayers || sideLayers.includes(entry.layer)) && (effectiveFilter === "failed" ? !entry.pass_k && !entry.skipped : effectiveFilter === "all" || entry.layer === effectiveFilter))
    .filter(entry => `${entry.id} ${entry.layer} ${entry.session} ${entry.split}`.toLowerCase().includes(caseSearch.trim().toLowerCase()))
    .sort(failedFirst);
  const usage = run.usage as Record<string, unknown> | undefined;
  return <section aria-label="运行详情">
    <div className={styles.sectionHead}><div><h2>{run.dir_name}</h2><p>{reqV2ModeLabels[run.mode] ?? run.mode} · {reqV2Time(run.created_at)}</p></div>
      <div className={styles.inlineActions}><Button variant="outline" onClick={openCompare}>选择两次运行对比</Button><Button variant="ghost" onClick={back}><ArrowLeft size={15} />{backLabel}</Button></div></div>
    <div className={styles.detailOverview} aria-label="运行状态">
      <div data-testid="reqv2-conclusion"><span>冻结门槛</span><strong className={run.gate_passed ? "status-pass" : run.gate_passed == null ? "status-unknown" : "status-fail"}>{conclusion}</strong></div>
      <div><span>证据状态</span><strong><EvidenceBadge evidence={run.evidence} /></strong></div>
      <div><span>运行范围</span><strong>{run.splits.join("/") || "split 未记录"} · {run.repeats} 次重复</strong></div>
    </div>
    <nav className={styles.detailTabs} aria-label="运行详情分区">{panels.map(([key, label]) => <button key={key} aria-current={panel === key ? "page" : undefined} onClick={() => setPanel(key)}>{label}</button>)}</nav>
    {run.evidence.notes.length > 0 && (!side || panel === "evidence" || run.evidence.status !== "complete") && <div className={run.evidence.status === "complete" ? styles.detailNotes : styles.notice} role={run.evidence.status === "complete" ? undefined : "alert"}>{run.evidence.notes.map((note, i) => <p key={i}>{note}</p>)}</div>}
    {panel === "results" && <section className={styles.turn} aria-label="报告结论"><h3>报告结论</h3>
      {side ? <details className={styles.versionDetails}><summary>报告原文</summary><p className={styles.muted}>{run.conclusion || "报告未写结论。"}</p></details> : <p className={styles.muted}>{run.conclusion || "报告未写结论。"}</p>}
      {run.limitations.length > 0 && <details className={styles.versionDetails}><summary>报告声明的限制({run.limitations.length} 条)</summary>{run.limitations.map((item, i) => <p key={i}>· {item}</p>)}</details>}
    </section>}
    {panel === "evidence" && <details className={styles.detailTechnical} open><summary>运行身份与完整性核对 · {run.integrity_checks.length} 项</summary>
      <dl className={styles.detailIdentity}>{identity.map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl>
      <p className={styles.muted}>核对的是证据完整性，不代表冻结门槛通过。</p>
      {run.integrity_checks.map((check, i) => <p key={i}>{check.state === "ok" ? "✓" : "×"} {check.check}{check.detail ? ` — ${check.detail}` : ""}</p>)}
    </details>}
    {panel === "results" && reqV2Sides.filter(item => !side || item.key === side).map(side => <section className={styles.turn} key={side.key} aria-label={`${side.title}跑分`}>
      <h3>{side.title} · 跑分</h3>
      {!sideLayers && <p className={styles.muted}>{side.hint}</p>}
      <div>{side.layers.map(layerScoreRow)}</div>
    </section>)}
    {panel === "gates" && <section className={styles.turn} aria-label="冻结门槛"><h3>冻结门槛({run.gate_verdicts.length} 项,来自冻结报告,不可在工作台修改)</h3>
      <p className={styles.muted}>确定性层(reducer / readiness / policy / ui-contract)要求 100%,已折算进上方总结论,不逐层出门槛行。</p>
      {gateGroups.map(gateGroup)}
    </section>}
    {panel === "dataset" && <section className={styles.turn} aria-label="评估集"><h3>评估集覆盖范围</h3>
      {run.manifest ? <DatasetCoverage manifest={run.manifest} runContext /> : <p className={styles.muted}>评估集信息未记录，或保存的文件无法读取。</p>}
    </section>}
    {panel === "prompts" && <section className={styles.turn} aria-label="模型与提示词"><h3>模型与提示词</h3>
      {run.models.length
        ? run.models.map(m => <p key={m.role}><b>{m.role}</b> {m.model}{m.provider ? ` · ${m.provider}` : ""}{m.reasoning_effort ? ` · reasoning ${m.reasoning_effort}` : ""}{m.timeout ? ` · 超时 ${m.timeout}` : ""}{m.session_cache ? " · 会话缓存" : ""}</p>)
        : zeroModel ? <p className={styles.muted}>未配置模型(零模型运行)。</p> : <p className={styles.muted}>模型未记录。</p>}
      {run.plan_note
        ? <><p className={styles.muted}>{run.prompt_sha256 ? "本次运行已声明保存提示词原文，可在提示词迭代中查看；文件通过指纹校验后才展示。" : "本次运行未保存提示词原文。events.jsonl 只记录逐请求哈希，无法从哈希还原文本；可在提示词迭代中查看 Git 历史原文。"}</p><details className={styles.versionDetails}><summary>运行记录声明</summary><p>{run.plan_note}</p></details></>
        : !zeroModel && <p className={styles.muted}>提示词记录策略未在 plan.json 声明。</p>}
    </section>}
    {panel === "evidence" && <section className={styles.turn} aria-label="效率与限制"><h3>调用与用量</h3>
      {zeroModel
        ? <p className={styles.muted}>零模型运行:模型调用、provider 成功率与延迟不适用,不在此显示。</p>
        : usage != null
          ? <details className={styles.versionDetails} open><summary>模型调用与 token</summary><pre className="whitespace-pre-wrap break-all text-xs">{JSON.stringify(usage, null, 1)}</pre></details>
          : <p className={styles.muted}>用量未记录。</p>}
    </section>}
    {["results", "dataset"].includes(panel) && <section className={styles.turn} aria-label="逐题索引"><h3>{panel === "dataset" ? "题目设计与冻结期望" : "逐题证据"} <span>{caseRows.length} 题</span></h3>
      {panel === "dataset" && <p className={styles.muted}>下方仅列出本次测试涉及的题目；完整题库规模见上方，未使用的用途分组已单独标注。</p>}
      <label className={styles.search}><Search size={16} aria-hidden /><span className="sr-only">搜索题目</span><input value={caseSearch} onChange={e => setCaseSearch(e.target.value)} placeholder="搜索题号、层、会话或 split" /></label>
      <div className={styles.filters} aria-label="题目过滤">
        {panel !== "dataset" && <button aria-pressed={effectiveFilter === "failed"} onClick={() => setLayerFilter("failed")}>未通过 Pass^k 优先</button>}
        <button aria-pressed={effectiveFilter === "all"} onClick={() => setLayerFilter("all")}>全部层</button>
        {layerOrder.filter(layer => layer in run.per_layer && (!sideLayers || sideLayers.includes(layer))).map(layer => <button key={layer} aria-pressed={effectiveFilter === layer} onClick={() => setLayerFilter(layer)}>{reqV2LayerLabels[layer] ?? layer}</button>)}
      </div>
      <div>{caseRows.map(entry => <button key={`${entry.layer}/${entry.id}`} className={`${styles.caseGrid} ${styles.caseRow}`} onClick={() => openCase(entry.layer, entry.id)}>
        <span><b>{entry.id}</b><small className={styles.block}>{reqV2LayerLabels[entry.layer] ?? entry.layer} · {entry.split}</small></span>
        <span><span className={styles.mobileLabel}>repeats</span>{entry.repeats.join(", ")}</span>
        <span>{entry.skipped ? <span className="status-unknown">已跳过 · 未评估</span> : entry.pass_k ? <span className="status-pass">Pass^k 通过</span> : <span className="status-fail">Pass^k 未通过</span>}{entry.vetoes > 0 && <small className="status-fail block">veto ×{entry.vetoes}</small>}</span>
        <ChevronRight className={styles.rowChevron} size={16} aria-hidden />
      </button>)}
      {!caseRows.length && <div className={styles.empty}>此过滤没有题目。</div>}</div>
    </section>}
  </section>;
}

function CaseDetail({ data, back }: { data: NonNullable<ReturnType<typeof useQuery<ReqV2CaseDetail>>["data"]>; back: () => void }) {
  const failing = data.repeats.find(repeat => !repeat.pass) ?? data.repeats[0];
  const [selected, setSelected] = useState<number | null>(null);
  const repeat = data.repeats.find(r => r.repeat === selected) ?? failing;
  return <section aria-label="逐题证据" className={styles.inspector}>
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

// 对比结果的分层增减按两侧展示;确定性层不进入模型统计,模型层行单独标出。
function SideLayerCompare({ outcome }: { outcome: ReqV2CompareOutcome }) {
  return <>{reqV2Sides.map(side => {
    const parts = [
      ...Object.entries(outcome.model_layers ?? {}).filter(([layer]) => side.layers.includes(layer)).map(([layer, d]) => `模型层 ${reqV2LayerLabels[layer] ?? layer} ${d.baseline_pass}/${d.total} → ${d.candidate_pass}/${d.total}`),
      ...Object.entries(outcome.deterministic_layers ?? {}).filter(([layer]) => side.layers.includes(layer)).map(([layer, d]) => `确定性层 ${reqV2LayerLabels[layer] ?? layer} ${d.baseline_pass}/${d.total} → ${d.candidate_pass}/${d.total}(不进入模型统计)`),
    ];
    if (!parts.length) return null;
    return <p key={side.key} className={styles.muted}>{side.title}:{parts.join(";")}</p>;
  })}</>;
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
  return <section aria-label="运行对比">
    <div className={styles.sectionHead}><div><h2>同身份对比</h2><p>严格对比要求判卷/manifest/gates/split/repeats 与运行语义一致;否则只并列查看。</p></div>
      <Button variant="outline" onClick={back}><ArrowLeft size={15} />返回运行目录</Button></div>
    <div className={styles.selection}>
      {[["va", "基线"], ["vb", "候选"]].map(([role, label]) => <label key={role} className={styles.selectLabel}><span>{label}运行</span>
        <select aria-label={`${label}运行`} value={role === "va" ? a : b} onChange={e => select(role as "va" | "vb", e.target.value)}><option value="">选择运行</option>{runs.map(run => <option key={run.id} value={run.id}>{`${reqV2Time(run.created_at)} · ${run.dir_name}`}</option>)}</select></label>)}
    </div>
    {(!a || !b) && <p className={styles.empty}>请选择基线与候选运行，系统会先核对冻结身份。</p>}
    {a && b && compare.isPending && <p className={styles.empty} role="status">正在核对两次运行的冻结身份…</p>}
    {compare.error && <ErrorMessage error={compare.error} retry={() => void compare.refetch()} />}
    {compare.data && <>
      {!compare.data.strict && <div className={styles.notice} role="alert" data-testid="compare-incomparable"><b>不具备严格比较条件;以下只并列证据,不给出改进/退步幅度。</b><ul>{compare.data.reasons?.map((reason, i) => <li key={i}>{reason}</li>)}</ul></div>}
      {compare.data.strict && compare.data.outcome && <div data-testid="compare-outcome">
        <p>共同题 {compare.data.case_set.common} · 模型层配对 {compare.data.outcome.model_pairs}</p>
        <p>持续通过 {compare.data.outcome.still_pass} · 持续失败 {compare.data.outcome.still_fail} · 退步 {compare.data.outcome.regressed.length} · 改善 {compare.data.outcome.newly_passing.length}</p>
        {compare.data.outcome.mcnemar_p != null && <p>McNemar 精确检验 p = {compare.data.outcome.mcnemar_p}</p>}
        {compare.data.outcome.sample_note && <p className={styles.notice}>{compare.data.outcome.sample_note}</p>}
        {compare.data.outcome.mcnemar_note && <p className={styles.muted}>{compare.data.outcome.mcnemar_note}</p>}
        {compare.data.strict && compare.data.outcome && <SideLayerCompare outcome={compare.data.outcome} />}
        {!!compare.data.outcome.regressed.length && <details open className={styles.versionDetails}><summary>退步题目</summary>{compare.data.outcome.regressed.map(key => <p key={key}>{key}</p>)}</details>}
        {!!compare.data.outcome.newly_passing.length && <details className={styles.versionDetails}><summary>新通过题目</summary>{compare.data.outcome.newly_passing.map(key => <p key={key}>{key}</p>)}</details>}
      </div>}
      <div className={styles.sideBySide}>
        {[compare.data.baseline, compare.data.candidate].map((run, index) => <div key={index}><h3>{run.dir_name}</h3>
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
  const area = params.get("area");
  const section: WorkspaceSection = params.get("view") === "compare" ? "compare" : workspaceSections.find(item => item.key === area)?.key ?? (runId ? "runs" : "screening");
  const selectedSection = workspaceSections.find(item => item.key === section)!;
  const comparing = section === "compare";
  const panel = ["results", "gates", "dataset", "prompts", "evidence"].includes(params.get("panel") ?? "") ? params.get("panel")! : "results";
  const selectSection = (key: WorkspaceSection) => navigate({ area: key, run: null, case: null, layer: null, panel: null, view: null, va: null, vb: null, ...(key !== section ? { test_purpose: null, test_mode: null, test_result: null } : {}) });
  const openRun = (id: string, detailPanel = "results") => navigate({ area: section, run: id, panel: detailPanel, case: null, layer: null });
  const back = () => navigate({ area: section === "compare" ? "runs" : section, run: null, case: null, layer: null, panel: null, view: null });
  const run = useQuery({ queryKey: ["evaldesk", "reqv2", "run", runId], queryFn: () => reqv2.run(runId), enabled: !!runId, retry: false, refetchOnWindowFocus: false });
  const caseDetail = useQuery({ queryKey: ["evaldesk", "reqv2", "case", runId, layer, caseId], queryFn: () => reqv2.case(runId, layer, caseId), enabled: !!runId && !!layer && !!caseId, retry: false, refetchOnWindowFocus: false });
  const reread = () => {
    void queryClient.invalidateQueries({ queryKey: ["evaldesk", "reqv2"] });
    onReread();
  };
  return <div className={styles.workspaceBody}><WorkspaceNav active={section} select={selectSection} /><main className={styles.content}><div className={styles.titleRow}><div>{section !== "screening" && section !== "builder" && <p className={styles.breadcrumb}>评估工作台 / {selectedSection.group}</p>}<h1 id="eval-page-heading" tabIndex={-1}>{selectedSection.label}</h1>{selectedSection.hint && <p>{selectedSection.hint}</p>}</div>
    <Button variant="outline" onClick={reread}><RefreshCw size={15} />重新读取</Button></div>
    {runs.data?.warnings.map((warning, i) => <p key={i} className={styles.notice}>{warning}</p>)}
    {runId && !comparing && run.isPending && <p className={styles.empty} role="status">正在读取运行详情…</p>}
    {runId && !comparing && run.error && <ErrorMessage error={run.error} retry={() => void run.refetch()} />}
    {runId && run.data && !caseId && !comparing && <RunDetail key={runId} run={run.data} panel={panel} setPanel={value => navigate({ panel: value })} side={section === "screening" || section === "builder" ? section : undefined} back={back} backLabel={section === "runs" ? "返回运行目录" : `返回${selectedSection.label}`} openCase={(caseLayer, id) => navigate({ layer: caseLayer, case: id })} openCompare={() => navigate({ area: "compare", view: null, run: null, panel: null, va: runId, vb: null })} />}
    {runId && layer && caseId && !comparing && (caseDetail.isPending
      ? <p className={styles.empty} role="status">正在读取逐题证据…</p>
      : caseDetail.error
        ? <ErrorMessage error={caseDetail.error} retry={() => void caseDetail.refetch()} />
        : caseDetail.data && <CaseDetail data={caseDetail.data} back={() => navigate({ case: null, layer: null })} />)}
    {comparing && runs.data && <CompareView params={params} runs={runs.data.runs} back={back} />}
    {!runId && section !== "runs" && runs.isPending && <p className={styles.empty} role="status">正在读取评估工作区…</p>}
    {!runId && section !== "runs" && runs.error && <ErrorMessage error={runs.error} retry={reread} />}
    {!runId && !comparing && runs.data && <>
      {(section === "screening" || section === "builder") && <SideWorkspace key={section} sideKey={section} runs={runs.data.runs} openRun={openRun} select={selectSection} params={params} navigate={navigate} />}
      {section === "datasets" && <DatasetWorkspace runs={runs.data.runs} openRun={openRun} params={params} navigate={navigate} />}
      {section === "prompts" && <PromptWorkspace runs={runs.data.runs} openRun={openRun} params={params} navigate={navigate} />}
    </>}
    {!runId && section === "runs" && <RunCatalog runs={runs.data} pending={runs.isPending} error={runs.error ?? undefined} openRun={openRun} onReread={reread} />}
  </main></div>;
}
