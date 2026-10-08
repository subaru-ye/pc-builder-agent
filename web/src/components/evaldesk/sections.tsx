"use client";

import { ArrowRight, BookOpen, Check, GitCompareArrows, ListChecks, MessageSquareText, ScanLine, Wrench, X } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { reqV2ModeLabels, reqV2Sides, reqV2Time, type ReqV2RunSummary } from "@/lib/evaldesk/reqv2";
import { groupRunExperiments, runPurposes } from "@/lib/evaldesk/run-experiments";
import styles from "./workbench.module.css";
import { PromptVersionsView } from "./prompts";

export const workspaceSections = [
  { key: "screening", label: "Screening 初筛", group: "评估对象", icon: ScanLine, hint: "" },
  { key: "builder", label: "Builder 选配", group: "评估对象", icon: Wrench, hint: "当前仅评估选配准入与界面行为，未覆盖配件选择质量。" },
  { key: "datasets", label: "评估集与题目", group: "迭代资产", icon: BookOpen, hint: "浏览题目、追踪内容迭代与关联测试。" },
  { key: "prompts", label: "提示词迭代", group: "迭代资产", icon: MessageSquareText, hint: "浏览历史版本、对比原文改动与查看关联测试。" },
  { key: "runs", label: "测试记录", group: "验证", icon: ListChecks, hint: "筛选真实调用、确定性检查与重放记录，定位失败和证据缺口。" },
  { key: "compare", label: "运行对比", group: "验证", icon: GitCompareArrows, hint: "选择基线与候选，核对可比性及逐题变化。" },
] as const;
export type WorkspaceSection = typeof workspaceSections[number]["key"];
export type OpenRun = (id: string, panel?: string) => void;

export function WorkspaceNav({ active, select }: { active: WorkspaceSection; select: (key: WorkspaceSection) => void }) {
  return <nav className={styles.workspaceNav} aria-label="评估工作导航">
    <div className={styles.navTitle}>评估工作台</div>
    {workspaceSections.map((section, index) => <div key={section.key}>
      {(index === 0 || workspaceSections[index - 1].group !== section.group) && <p className={styles.navGroup}>{section.group}</p>}
      <button aria-current={active === section.key ? "page" : undefined} onClick={() => select(section.key)}><section.icon size={17} aria-hidden />{section.label}</button>
    </div>)}
  </nav>;
}

function Score({ run, layer }: { run: ReqV2RunSummary; layer: string }) {
  const row = run.layer_scores?.[layer];
  if (!row || row.cases === 0) return <span className={styles.muted}>未评估</span>;
  return <span>{row.passed}/{row.cases}{row.vetoes > 0 && <small className="status-fail"> · veto {row.vetoes}</small>}</span>;
}

const splitNames: Record<string, string> = { development: "开发集", calibration: "校准集", holdout: "保留集" };
const sideLayerNames: Record<string, string> = { extraction: "需求抽取", conversations: "多轮对话", reducer: "需求更新", readiness: "信息完整性", policy: "选配准入", "ui-contract": "界面行为" };
const testScope = (run: ReqV2RunSummary) => `${reqV2ModeLabels[run.mode] ?? run.mode} · ${run.splits.map(split => splitNames[split] ?? split).join(" + ")} · 每题 ${run.repeats} 次`;
const gateNames: Record<string, string> = { task_success: "多轮任务成功率", latency_p95_ms: "P95 延迟", repeated_question_max_per_case: "单题重复追问", repeated_questions: "重复追问", key_field_wrong_write_total: "关键字段错写", min_cases: "最低题量" };

function FailedGates({ run }: { run: ReqV2RunSummary }) {
  if (run.gate_passed !== false) return null;
  const failed = run.failed_gates?.filter(gate => gate.evaluable) ?? [];
  const unavailable = run.failed_gates?.filter(gate => !gate.evaluable) ?? [];
  const rows = (gates: NonNullable<ReqV2RunSummary["failed_gates"]>) => gates.map((gate, i) => <small key={i}>{gateNames[gate.metric] ?? gate.metric}：{gate.actual} / 要求 {gate.threshold}{!gate.evaluable && "（不可评估）"}</small>);
  return <div className={styles.failedGateList}>{rows(failed)}{unavailable.length > 0 && <details className={styles.experimentReference}><summary>{unavailable.length} 项门槛不可评估</summary>{rows(unavailable)}</details>}{!run.failed_gates?.length && <small>未通过项见详情</small>}</div>;
}

function RunGates({ run, builder }: { run: ReqV2RunSummary; builder: boolean }) {
  const status = <span className={run.gate_passed == null ? styles.muted : run.gate_passed ? "status-pass" : "status-fail"}>{builder && "整次运行："}{run.gate_passed == null ? "门槛未记录" : <>{run.gate_passed ? <Check size={14} aria-hidden /> : <X size={14} aria-hidden />}{run.gate_passed ? "门槛通过" : "门槛未通过"}</>}</span>;
  return builder && run.gate_passed === false
    ? <details className={styles.experimentReference}><summary>{status}</summary><FailedGates run={run} /></details>
    : <>{status}<FailedGates run={run} /></>;
}

type SideWorkspaceProps = { sideKey: "screening" | "builder"; runs: ReqV2RunSummary[]; openRun: OpenRun; select: (key: WorkspaceSection) => void; params: URLSearchParams; navigate: (updates: Record<string, string | null>) => void };

export function SideWorkspace({ sideKey, runs, openRun, select, params, navigate }: SideWorkspaceProps) {
  const side = reqV2Sides.find(item => item.key === sideKey)!;
  const builder = sideKey === "builder";
  const purposes = runPurposes.map(item => ({ ...item, label: builder && item.key === "models" ? "模型实验检查" : item.label }));
  const groupTitle = (group: ReturnType<typeof groupRunExperiments>[number]) => builder && group.id === "models-20260924" ? "不同初筛模型下的选配检查 · 09-24" : group.title;
  const relevant = runs.filter(run => !run.superseded && side.layers.some(layer => (run.layer_scores?.[layer]?.cases ?? 0) > 0));
  const groups = groupRunExperiments(relevant);
  const purpose = runPurposes.some(item => item.key === params.get("test_purpose")) ? params.get("test_purpose")! : "all";
  const mode = params.get("test_mode") ?? "all";
  const visible = groups.filter(group => purpose === "all" || group.purpose === purpose).map(group => ({ ...group, runs: group.runs.filter(({ run }) => mode === "all" || run.mode === mode) })).filter(group => group.runs.length);
  const entries = visible.flatMap(group => group.runs.map(entry => ({ ...entry, group })));
  const latestEntries = entries.filter(entry => entry.group.id === visible[0]?.id).sort((a, b) => (b.run.created_at ?? "").localeCompare(a.run.created_at ?? ""));
  const defaultEntry = visible[0]?.purpose === "models" ? latestEntries.find(entry => entry.baseline) ?? latestEntries[0] : latestEntries[0];
  const selected = entries.find(entry => entry.run.id === params.get("test_result")) ?? defaultEntry;
  const latest = selected?.run;
  const count = (key: string) => new Set(groups.filter(group => key === "all" || group.purpose === key).flatMap(group => group.runs.map(({ run }) => run.id))).size;
  const purposeLabel = purposes.find(item => item.key === purpose)?.label ?? "全部测试";
  const changeFilter = (updates: Record<string, string | null>) => navigate({ ...updates, test_result: null });
  const selectResult = (id: string) => {
    navigate({ test_result: id });
    document.getElementById(`${sideKey}-selected-result`)?.scrollIntoView({ block: "start" });
  };
  return <>
    <section aria-label="测试目的">
      <div className={styles.catalogHeading}><h2>测试记录 <span>{relevant.length}</span></h2><Button variant="ghost" onClick={() => select("runs")}>全部记录<ArrowRight size={14} /></Button></div>
      <div className={styles.experimentToolbar}>
        <div className={styles.filters} aria-label="测试目的分类">
          {[{ key: "all", label: "全部" }, ...purposes].filter(item => item.key === "all" || count(item.key) > 0).map(item => <button key={item.key} aria-pressed={purpose === item.key} onClick={() => changeFilter({ test_purpose: item.key === "all" ? null : item.key, test_mode: null })}>{item.label}<span>{count(item.key)}</span></button>)}
        </div>
        <label className={styles.selectLabel}><span>执行方式</span><select value={mode} onChange={e => changeFilter({ test_mode: e.target.value === "all" ? null : e.target.value })}><option value="all">全部方式</option>{["live", "deterministic", "replay", "regrade"].map(value => <option key={value} value={value}>{reqV2ModeLabels[value]}</option>)}</select></label>
      </div>
    </section>
    {selected && <section aria-label="最近分层结果" id={`${sideKey}-selected-result`} className={styles.selectedResult}>
      <div className={styles.catalogHeading}><h2>结果概览</h2><span>{reqV2Time(selected.run.created_at)}</span></div>
      <div className={styles.selectedExperiment}><b>{selected.label}{!builder && ` · ${selected.run.models.find(model => model.role === "screening")?.model ?? (selected.run.zero_model ? "程序检查" : "模型未记录")}`}</b><p>{groupTitle(selected.group)} · {testScope(selected.run)}</p>{selected.run.evidence.status !== "complete" && <small className="status-review">证据需核对</small>}</div>
      <div className={`${styles.layerStrip} ${builder || selected.run.zero_model ? styles.twoLayers : ""}`}>{side.layers.filter(layer => builder || !selected.run.zero_model || (selected.run.layer_scores?.[layer]?.cases ?? 0) > 0).map(layer => <div key={layer}><span>{sideLayerNames[layer]}</span><strong><Score run={selected.run} layer={layer} /></strong>{!builder && <small>{["extraction", "conversations"].includes(layer) ? "模型" : "程序"}</small>}</div>)}</div>
      <div className={styles.inlineActions}><Button variant="outline" onClick={() => openRun(selected.run.id)}>查看结果<ArrowRight size={14} /></Button></div>
    </section>}
    <section className={styles.turn} aria-label={builder ? "选配测试实验" : "初筛测试实验"}>
      <div className={styles.catalogHeading}><h2>实验批次 <span>{visible.length}</span></h2></div>
      {visible.map((group, index) => {
        const baseline = group.runs.find(entry => entry.baseline)?.run;
        const scopes = new Set(group.runs.map(({ run }) => testScope(run)));
        return <details className={styles.experimentGroup} data-testid={`experiment-${group.id}`} key={`${purpose}-${mode}-${group.id}`} open={index === 0 || undefined}>
          <summary><span><b>{groupTitle(group)}</b><small>{group.runs.length} 条记录{scopes.size === 1 ? ` · ${[...scopes][0]}` : ""}</small></span></summary>
          <div className={styles.experimentColumns} aria-hidden><span>{builder ? "测试批次" : "测试 / 模型"}</span><span>通过题数</span><span>{builder ? "整次运行 / 操作" : "总门槛 / 操作"}</span></div>
          {group.runs.map(({ run, label, baseline: isBaseline }) => <div className={styles.experimentRun} data-testid={`experiment-run-${run.dir_name}`} key={run.id} data-selected={latest?.id === run.id || undefined}>
            <div><b>{group.purpose === "unknown" ? run.dir_name : label}</b>{run.models.some(model => model.role === "screening") && <p>{builder && "初筛模型："}{run.models.find(model => model.role === "screening")?.model}</p>}<small>{reqV2Time(run.created_at)}</small>{scopes.size > 1 && <small>{testScope(run)}</small>}</div>
            <div className={styles.layerResults}>{side.layers.filter(layer => builder || (run.layer_scores?.[layer]?.cases ?? 0) > 0 && (run.zero_model || ["extraction", "conversations"].includes(layer))).map(layer => <span key={layer}><small>{sideLayerNames[layer]}</small><Score run={run} layer={layer} /></span>)}</div>
            <div><RunGates run={run} builder={builder} />{run.evidence.status !== "complete" && <small className="status-review">证据需核对</small>}<div className={styles.inlineActions}><Button variant="ghost" aria-pressed={latest?.id === run.id} onClick={() => selectResult(run.id)}>查看分层</Button><Button variant="ghost" onClick={() => openRun(run.id)}>查看详情</Button>{baseline && !isBaseline && <Button variant="ghost" onClick={() => navigate({ area: "compare", view: null, run: null, va: baseline.id, vb: run.id })}>与基线对比</Button>}</div></div>
          </div>)}
          <details className={styles.experimentReference}><summary>测试说明</summary>{builder && <p>按整次运行的测试目的分类；本页题数仅反映选配准入与界面行为。总门槛包含初筛等其他检查。</p>}<p>{group.question}</p><p>{group.note}</p><p>题目需在全部重复测试中通过，才计为通过。可比性由运行对比页核对。</p>{group.reference && <p>{group.reference}</p>}</details>
        </details>;
      })}
      {!visible.length && <div className={styles.empty} role="status">{mode !== "all" && count(purpose) > 0 ? <><p>“{purposeLabel}”有 {count(purpose)} 条记录，当前“{reqV2ModeLabels[mode] ?? mode}”筛选没有匹配项。</p><Button variant="outline" onClick={() => changeFilter({ test_mode: null })}>清除执行方式筛选</Button></> : <p>当前分类没有测试记录。</p>}</div>}
    </section>
  </>;
}

export function PromptWorkspace({ runs, openRun, params, navigate }: { runs: ReqV2RunSummary[]; openRun: OpenRun; params: URLSearchParams; navigate: (updates: Record<string, string | null>) => void }) {
  const role = params.get("prompt_role") === "builder" ? "builder" : "screening";
  const history = params.get("prompt_view") === "history";
  const [includeArchived, setIncludeArchived] = useState(false);
  const [search, setSearch] = useState("");
  const visible = runs.filter(run => (includeArchived || !run.superseded) && !run.zero_model && run.models.some(model => model.role === role) && `${run.dir_name} ${run.code_commit} ${run.models.map(model => model.model).join(" ")}`.toLowerCase().includes(search.trim().toLowerCase()));
  return <>
    <div className={styles.filters} aria-label="提示词角色">{["screening", "builder"].map(value => <button key={value} aria-pressed={role === value} onClick={() => navigate({ prompt_role: value, prompt_component: null, prompt_version: null, prompt_base: null })}>{value === "screening" ? "Screening 初筛" : "Builder 选配"}</button>)}</div>
    <nav className={styles.filters} aria-label="提示词视图"><button aria-pressed={!history && !["compare", "iterations"].includes(params.get("prompt_view") ?? "")} onClick={() => navigate({ prompt_view: null })}>版本与原文</button><button aria-pressed={params.get("prompt_view") === "iterations"} onClick={() => navigate({ prompt_view: "iterations" })}>迭代记录</button><button aria-pressed={params.get("prompt_view") === "compare"} onClick={() => navigate({ prompt_view: "compare" })}>版本对比</button><button aria-pressed={history} onClick={() => navigate({ prompt_view: "history" })}>历史测试</button></nav>
    {!history && <PromptVersionsView role={role} params={params} navigate={navigate} runs={runs} openRun={openRun} />}
    {history && <>
    <div className={styles.scopeNote}><b>历史测试记录</b><p>旧测试未保存运行时原文，代码提交不等于提示词版本。可在「版本与原文」查看 Git 历史；新测试会保存原文并关联到内容版本。</p></div>
    <div className={styles.catalogToolbar}><label className={styles.search}><span className="sr-only">搜索提示词迭代</span><input value={search} onChange={e => setSearch(e.target.value)} placeholder="搜索模型、运行或代码提交" /></label><label className={styles.checkbox}><input type="checkbox" checked={includeArchived} onChange={e => setIncludeArchived(e.target.checked)} />包含归档</label></div>
    <div className={styles.catalogHeading}><h2>关联测试 <span>{visible.length}</span></h2><span>按运行时间倒序</span></div>
    {visible.map(run => <section className={styles.assetRow} key={run.id}>
      <div><h3>{run.models.filter(model => model.role === role).map(model => model.model).join(" · ")}</h3><p className={styles.muted}>{reqV2Time(run.created_at)} · {reqV2ModeLabels[run.mode] ?? run.mode} · {run.dir_name}</p><p>代码 {run.code_commit?.slice(0, 12) || "未记录"} · {run.code_dirty == null ? "工作区状态未知" : run.code_dirty ? "含未提交修改，无法仅凭提交复现" : "干净工作区"}</p><small className={styles.muted}>{run.models.filter(model => model.role === role).map(model => `reasoning ${model.reasoning_effort || "未记录"} · 超时 ${model.timeout || "未记录"}`).join(" · ")}</small></div>
      <div><p className={styles.muted}>{run.splits.join("/")}</p><p>{run.prompt_sha256 ? "已声明保存原文" : "运行原文未保存"}</p><Button variant="outline" onClick={() => openRun(run.id, "prompts")}>查看配置与运行证据<ArrowRight size={14} /></Button></div>
    </section>)}
    {!visible.length && <p className={styles.empty}>{role === "builder" ? "此评估库尚无真实 Builder 模型运行，不能生成提示词迭代记录。" : "没有匹配的模型运行，请调整筛选或先完成一次评估。"}</p>}
    </>}
  </>;
}
