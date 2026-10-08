"use client";

import { useQuery } from "@tanstack/react-query";
import { ArrowRight, BookOpen, ChevronDown, ScanLine, Search, Wrench } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { reqv2, reqV2LayerLabels, reqV2ModeLabels, reqV2Time, type ReqV2FrozenCase, type ReqV2Manifest, type ReqV2RunSummary } from "@/lib/evaldesk/reqv2";
import type { OpenRun } from "./sections";
import styles from "./workbench.module.css";

const topics: Record<string, { name: string; purpose: string }> = {
  extraction: { name: "需求抽取", purpose: "能否读懂预算、用途与偏好" },
  conversations: { name: "多轮沟通", purpose: "能否追问缺失信息并记住上下文" },
  reducer: { name: "需求更新", purpose: "修改、撤销需求后，状态是否正确" },
  readiness: { name: "信息完整性", purpose: "能否判断需求是否已具备选配条件" },
  policy: { name: "选配准入", purpose: "是否只在满足确认条件后启动选配" },
  "ui-contract": { name: "界面交互", purpose: "界面状态与可用操作是否符合约定" },
};
const splitLabels: Record<string, { name: string }> = {
  development: { name: "回归测试" },
  calibration: { name: "阈值校准" },
  holdout: { name: "保留验证" },
};
const auxiliary = new Set(["selftest.json", "catalog.json"]);
const splitHelp: Record<string, string> = { development: "日常修改后检查是否引入问题", calibration: "用于校准评估标准与参数", holdout: "独立验证，不参与日常调优" };
const topicName = (layer: string) => topics[layer]?.name ?? reqV2LayerLabels[layer] ?? layer;
const datasetTitle = (manifest: ReqV2Manifest) => manifest.dataset === "requirement-v2" ? "需求理解与选配准入" : manifest.dataset || "未命名评估集";
const questionFiles = (manifest: ReqV2Manifest) => manifest.files.filter(file => !auxiliary.has(file.layer));
const editionLabel = (firstSeen: string | null) => {
  const time = reqV2Time(firstSeen);
  return time === "时间未记录" ? "日期未记录" : `${time.slice(5, 7)}月${time.slice(8, 10)}日 ${time.slice(11)}`;
};

// 这里只比较保存的清单元数据，不推断逐题增删或评估效果。
function manifestChanges(current: ReqV2Manifest, previous: ReqV2Manifest): string[] {
  const changes: string[] = [];
  for (const layer of new Set([...previous.files, ...current.files].map(file => file.layer))) {
    const before = previous.files.find(file => file.layer === layer);
    const after = current.files.find(file => file.layer === layer);
    if (!before || !after) changes.push(`${topicName(layer)}：${after ? "新增分类或文件" : "移除分类或文件"}`);
    else if (before.cases !== after.cases) changes.push(`${topicName(layer)}：${before.cases} → ${after.cases} 项`);
    else if (before.sha256 && after.sha256 && before.sha256 !== after.sha256) changes.push(`${topicName(layer)}：内容变更，数量不变`);
    else if (!before.sha256 || !after.sha256) changes.push(`${topicName(layer)}：缺少内容指纹，无法核对是否变化`);
  }
  if (current.grader_version !== previous.grader_version) changes.push("评分规则版本变更");
  const beforeSplits = new Map(previous.splits.map(split => [split.split, split.sessions]));
  if (current.splits.length !== previous.splits.length || current.splits.some(split => beforeSplits.get(split.split) !== split.sessions)) changes.push("用途分组的会话数量变更");
  return changes.length ? changes : ["已记录的文件内容、题量与评分版本一致；内容标识变化不代表题目变化。"];
}

export function DatasetCoverage({ manifest, runContext = false }: { manifest: ReqV2Manifest; runContext?: boolean }) {
  const files = questionFiles(manifest);
  const total = files.reduce((sum, file) => sum + file.cases, 0);
  const max = Math.max(1, ...files.map(file => file.cases));
  const selftest = manifest.files.find(file => file.layer === "selftest.json");
  const groups = [
    { title: "Screening · 需求理解", icon: ScanLine, layers: ["extraction", "conversations", "reducer", "readiness"] },
    { title: "Builder · 选配准入", icon: Wrench, layers: ["policy", "ui-contract"] },
    { title: "其他评估内容", icon: BookOpen, layers: files.filter(file => !topics[file.layer]).map(file => file.layer) },
  ];
  return <>
    <div className={styles.datasetStats} aria-label="题库覆盖规模"><strong>{total}<span>道评估题</span></strong><span>{files.length} 个题目分类</span><span>保存于 {manifest.frozen_at || "日期未记录"}</span></div>
    <section className={styles.datasetSection} aria-label="题目覆盖范围">
      <div className={styles.coverageGroups}>{groups.map(group => {
        const rows = group.layers.flatMap(layer => files.filter(file => file.layer === layer));
        if (!rows.length) return null;
        return <section key={group.title} aria-label={group.title}><h4><group.icon size={18} aria-hidden /><b>{group.title}</b><span>{rows.reduce((sum, file) => sum + file.cases, 0)} 题</span></h4>
          <div className={styles.coverageRows}>{rows.map(file => <div className={styles.coverageRow} key={file.layer} data-testid={`manifest-${file.layer}`}>
            <div><b>{topicName(file.layer)}</b><span>{file.cases} 题</span></div><p>{topics[file.layer]?.purpose ?? "该分类尚未提供用途说明"}</p>
            <div className={styles.coverageTrack} aria-hidden><span style={{ width: `${file.cases / max * 100}%` }} /></div>
          </div>)}</div>
        </section>;
      })}</div>
    </section>
    <details className={styles.versionDetails}><summary>分组与评分信息</summary>
    <section aria-label="题目用途分组">
      {manifest.splits.length ? <div className={styles.splitList}>{manifest.splits.map(split => <div key={split.split}>
        <b>{splitLabels[split.split]?.name ?? split.split}</b><span>{split.sessions} 个会话{runContext && <small>{split.used ? "本运行使用" : "本运行未使用"}</small>}</span>
      </div>)}</div> : <p className={styles.muted}>用途分组未记录。</p>}
    </section>
    <p>{selftest ? `评分器自检 ${selftest.cases} 项 · ` : ""}评分规则：{manifest.grader_version || "未记录"}</p>
    <details className={styles.versionDetails}><summary>文件校验信息</summary>
      {manifest.files.map(file => <p key={file.layer} data-testid={auxiliary.has(file.layer) ? `manifest-${file.layer}` : undefined}>{topicName(file.layer)} · {file.cases} 项 · {file.sessions} 会话 · 指纹 {file.sha256 || "未记录"}</p>)}
    </details>
    </details>
  </>;
}

const designLabels: Record<string, string> = {
  expected: "预期结果", expect: "预期结果", expected_operations: "应记录的需求", forbidden_operations: "禁止的操作",
  expected_turn_signals: "对话意图", expected_missing_fields: "仍需补充", expected_confirmation_eligible: "允许核定",
  expected_next_action_absent: "不得直接启动选配", expected_final_state: "最终需求状态", expected_revision_delta: "版本增量", revision_delta: "版本增量",
  builder_admission: "允许启动选配", admission_reason: "准入原因", presentation_action: "界面动作", forbidden_vetoes: "禁止触发的违规项",
  requirement_readiness: "需求完整性", requirement_confirmation_status: "需求确认状态", build_relation: "配置状态",
  status: "状态", missing_fields: "缺失信息", blocking_conflicts: "阻塞冲突", confirmation_eligible: "允许核定",
  op: "操作", field: "字段", value: "值", quote: "来源原话", quote_contains: "来源需包含", strength: "要求强度",
  budget_cny: "预算", budget_flex: "预算弹性", existing_parts: "复用配件", "use_case.type": "用途", "use_case.titles": "游戏或软件",
  "use_case.resolution": "分辨率", "use_case.performance_goal": "性能目标", asks_question: "询问", requests_review: "请求核定",
  requests_build: "请求选配", ambiguous: "意图不明确", expected_state: "预期状态", expected_error: "预期错误",
  user_message: "用户输入", seed_user_message: "前置对话", seed: "前置需求", initial_state: "初始状态", operations: "需求操作",
  turn: "本轮输入", turns: "多轮输入", title: "场景名称", rationale: "设计依据", split: "用途分组", session: "会话编号",
  fields: "需求字段", source: "来源", kind: "类型", message_id: "消息编号", evidence: "证据", reply_next_action_absent: "回复不得直接启动选配",
  expected_reply_contains: "回复应包含", expected_reply_not_contains: "回复不得包含", expected_vetoes: "预期违规项", next_action: "下一步动作",
};
const valueLabels: Record<string, string> = { set: "设置", unset: "撤销", gaming: "游戏", fps_first: "帧率优先", ready: "齐备", incomplete: "待补充", unconfirmed: "未确认", confirmed: "已确认", none: "无", must: "必须", prefer: "偏好", active: "已生效", unknown: "未提供", revoked: "已撤销", stated: "用户明确表达", chat: "聊天", focus_missing_requirement: "补充缺失需求", open_requirement_review: "打开需求核定", readiness_incomplete: "需求未齐备" };
const record = (value: unknown): Record<string, unknown> | null => value != null && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : null;
function DesignValue({ value, depth = 0 }: { value: unknown; depth?: number }) {
  if (value == null) return <span className={styles.muted}>未设置</span>;
  if (typeof value === "boolean") return <span>{value ? "是" : "否"}</span>;
  if (Array.isArray(value)) return value.length ? <ul className={styles.designArray}>{value.map((item, index) => <li key={index}><DesignValue value={item} depth={depth} /></li>)}</ul> : <span className={styles.muted}>无</span>;
  if (typeof value === "object") return depth > 0 ? <details className={styles.versionDetails}><summary>详细条件</summary><pre>{JSON.stringify(value, null, 2)}</pre></details> : <dl className={styles.designFields}>{Object.entries(value).map(([key, item]) => <div key={key}><dt>{designLabels[key] ?? key}</dt><dd><DesignValue value={item} depth={depth + 1} /></dd></div>)}</dl>;
  return <span>{valueLabels[String(value)] ?? designLabels[String(value)] ?? String(value)}</span>;
}
function ExpectedFields({ value }: { value: unknown }) {
  const fields = record(value);
  if (!fields) return <DesignValue value={value} />;
  return <dl className={styles.designFields}>{Object.entries(fields).map(([key, item]) => {
    const field = record(item);
    const metadata = field ? Object.fromEntries(Object.entries(field).filter(([key]) => !["value", "status", "strength"].includes(key))) : {};
    return <div key={key}><dt>{designLabels[key] ?? key}</dt><dd>{field ? <>
      {"value" in field && <b><DesignValue value={field.value} />{key.endsWith("cny") && typeof field.value === "number" ? " 元" : ""}</b>}
      <small>{[field.status, field.strength].filter(Boolean).map(value => valueLabels[String(value)] ?? String(value)).join(" · ")}</small>
      {!!Object.keys(metadata).length && <details className={styles.versionDetails}><summary>来源与字段校验</summary><DesignValue value={metadata} /></details>}
    </> : <DesignValue value={item} />}</dd></div>;
  })}</dl>;
}
function ExpectedOperations({ value }: { value: unknown }) {
  if (!Array.isArray(value)) return <DesignValue value={value} />;
  return value.length ? <ul className={styles.expectedOperations}>{value.map((item, index) => {
    const op = record(item);
    if (!op) return <li key={index}><DesignValue value={item} /></li>;
    return <li key={index}><b>{valueLabels[String(op.op)] ?? String(op.op ?? "操作")} {designLabels[String(op.field)] ?? String(op.field ?? "字段")}</b>{"value" in op && <span><DesignValue value={op.value} />{String(op.field).endsWith("cny") && typeof op.value === "number" ? " 元" : ""}</span>}{op.quote_contains != null && <small>来源原话须包含“{String(op.quote_contains)}”</small>}<details className={styles.versionDetails}><summary>操作校验详情</summary><pre>{JSON.stringify(op, null, 2)}</pre></details></li>;
  })}</ul> : <span className={styles.muted}>无</span>;
}
function Expectations({ value }: { value: unknown }) {
  const expected = record(value);
  if (!expected) return <DesignValue value={value} />;
  const main = Object.entries(expected).filter(([key]) => key in designLabels);
  const other = Object.fromEntries(Object.entries(expected).filter(([key]) => !(key in designLabels)));
  return <>
    {main.map(([key, item]) => <section className={styles.expectationGroup} key={key}><h5>{designLabels[key]}</h5>{key === "fields" ? <ExpectedFields value={item} /> : ["expected_operations", "forbidden_operations"].includes(key) ? <ExpectedOperations value={item} /> : <DesignValue value={item} />}</section>)}
    {!!Object.keys(other).length && <details className={styles.versionDetails}><summary>其他判分条件 · {Object.keys(other).length} 项</summary><DesignValue value={other} /></details>}
  </>;
}
function QuestionDesign({ question }: { question: ReqV2FrozenCase }) {
  const fields = question.fields;
  const turns = Array.isArray(fields.turns) ? fields.turns as Record<string, unknown>[] : [];
  const turn = fields.turn && typeof fields.turn === "object" ? fields.turn as Record<string, unknown> : null;
  const input = fields.user_message ?? turn?.text;
  return <div className={styles.questionDesign}>
    <div className={styles.questionContext}><span>分类：{topicName(question.layer)}</span><span title={splitHelp[question.split ?? ""]}>用途：{splitLabels[question.split ?? ""]?.name ?? question.split ?? "未记录"}</span>{splitHelp[question.split ?? ""] && <small>{splitHelp[question.split!]}</small>}</div>
    {(input != null || (fields.expected ?? fields.expect) != null) && <div className={styles.questionComparison}>
      <section><h4>用户输入</h4>{input != null ? <blockquote>{String(input)}</blockquote> : <p className={styles.muted}>本题使用预置需求状态</p>}{fields.seed_user_message ? <details className={styles.versionDetails}><summary>前置对话</summary><blockquote>{String(fields.seed_user_message)}</blockquote></details> : null}</section>
      {(fields.expected ?? fields.expect) != null && <section><h4>预期结果与判分条件</h4><Expectations value={fields.expected ?? fields.expect} /></section>}
    </div>}
    {turns.map((item, index) => <section className={styles.questionTurn} key={index}><h4>第 {index + 1} 轮</h4><div className={styles.questionComparison}><section><h5>用户输入</h5><blockquote>{String(item.text ?? item.user_message ?? "输入未记录")}</blockquote></section><section><h5>预期结果</h5>{(item.expect ?? item.expected) != null ? <Expectations value={item.expect ?? item.expected} /> : <p className={styles.muted}>未记录</p>}</section></div></section>)}
    {question.rationale && <details className={styles.versionDetails}><summary>设计依据</summary><p>{question.rationale}</p></details>}
    <details className={styles.versionDetails}><summary>前置状态与题目原文</summary><pre>{JSON.stringify({ id: question.id, ...fields }, null, 2)}</pre></details>
  </div>;
}
const questionKey = (question: ReqV2FrozenCase) => `${question.layer}/${question.id}`;
function QuestionChanges({ current, previous }: { current: ReqV2FrozenCase[]; previous: ReqV2FrozenCase[] }) {
  const before = new Map(previous.map(question => [questionKey(question), question]));
  const after = new Map(current.map(question => [questionKey(question), question]));
  const changes = [...new Set([...after.keys(), ...before.keys()])].flatMap(key => {
    const a = before.get(key), b = after.get(key);
    if (a && b && a.content_sha256 === b.content_sha256) return [];
    return [{ key, a, b, label: !a ? "新增" : !b ? "移除" : "修改" }];
  });
  return <>
    <div className={styles.datasetStats}>{["新增", "修改", "移除"].map(label => <span key={label}>{label} <b>{changes.filter(change => change.label === label).length}</b> 题</span>)}</div>
    {!changes.length && <p className={styles.muted}>题目内容未变化。</p>}
    {changes.map(({ key, a, b, label }) => <details key={key} className={styles.questionChange}><summary><span>{label}</span><b>{(b ?? a)!.title || (b ?? a)!.id}</b><small>{topicName((b ?? a)!.layer)}</small><ChevronDown size={16} aria-hidden /></summary>
      {a && b && <p className={styles.catalogHelp}>变更项：{[...new Set([...Object.keys(a.fields), ...Object.keys(b.fields), "title", "rationale", "split", "session"])].filter(field => JSON.stringify(a.fields[field] ?? a[field as keyof ReqV2FrozenCase]) !== JSON.stringify(b.fields[field] ?? b[field as keyof ReqV2FrozenCase])).map(field => designLabels[field] ?? field).join("、")}</p>}
      <div className={styles.sideBySide}>{[["调整前", a], ["调整后", b]].map(([title, question]) => <div key={title as string}><h4>{title as string}</h4>{question ? <><p>{(question as ReqV2FrozenCase).title}</p><QuestionDesign question={question as ReqV2FrozenCase} /></> : <p className={styles.muted}>无此题目</p>}</div>)}</div>
    </details>)}
  </>;
}

export function DatasetWorkspace({ runs, openRun, params, navigate }: { runs: ReqV2RunSummary[]; openRun: OpenRun; params: URLSearchParams; navigate: (updates: Record<string, string | null>) => void }) {
  const includeArchived = params.get("dataset_archived") === "true";
  const view = ["history", "tests"].includes(params.get("dataset_view") ?? "") ? params.get("dataset_view")! : "questions";
  const [search, setSearch] = useState("");
  const side = params.get("dataset_side") === "builder" ? "builder" : "screening";
  const grouped = new Map<string, ReqV2RunSummary[]>();
  runs.forEach(run => { const key = run.manifest_sha256 || run.id; grouped.set(key, [...(grouped.get(key) ?? []), run]); });
  const groups = [...grouped].map(([key, items]) => ({ key, items, firstSeen: items.map(run => run.created_at).filter((value): value is string => !!value).sort()[0] ?? null }))
    .sort((a, b) => (b.firstSeen ?? "").localeCompare(a.firstSeen ?? "") || a.key.localeCompare(b.key))
    .filter(group => includeArchived || group.items.some(run => !run.superseded));
  const selected = groups.find(group => group.key === params.get("snapshot")) ?? groups[0];
  const previous = selected ? groups[groups.indexOf(selected) + 1] : undefined;
  const representative = (items: ReqV2RunSummary[] = []) => items.find(run => !run.superseded && run.evidence.status === "complete") ?? items.find(run => run.evidence.status === "complete") ?? items[0];
  const run = representative(selected?.items), olderRun = representative(previous?.items);
  const currentQuery = useQuery({ queryKey: ["evaldesk", "reqv2", "run", run?.id], queryFn: () => reqv2.run(run!.id), enabled: !!run, retry: false, staleTime: 30_000, refetchOnWindowFocus: false });
  const previousQuery = useQuery({ queryKey: ["evaldesk", "reqv2", "run", olderRun?.id], queryFn: () => reqv2.run(olderRun!.id), enabled: !!olderRun && view === "history", retry: false, staleTime: 30_000, refetchOnWindowFocus: false });
  const questions = useQuery({ queryKey: ["evaldesk", "reqv2", "dataset", run?.id], queryFn: () => reqv2.dataset(run!.id), enabled: !!run && !!currentQuery.data?.manifest && view !== "tests", retry: false, staleTime: 30_000, refetchOnWindowFocus: false });
  const olderQuestions = useQuery({ queryKey: ["evaldesk", "reqv2", "dataset", olderRun?.id], queryFn: () => reqv2.dataset(olderRun!.id), enabled: !!olderRun && view === "history", retry: false, staleTime: 30_000, refetchOnWindowFocus: false });
  const manifest = currentQuery.data?.manifest;
  const sideLayers = side === "builder" ? ["policy", "ui-contract"] : ["extraction", "conversations", "reducer", "readiness"];
  const layer = sideLayers.includes(params.get("dataset_layer") ?? "") ? params.get("dataset_layer")! : "all";
  const filtered = (questions.data?.cases ?? []).filter(question => sideLayers.includes(question.layer) && (layer === "all" || question.layer === layer) && `${question.title} ${question.id} ${question.rationale} ${JSON.stringify(question.fields)}`.toLowerCase().includes(search.trim().toLowerCase())).sort((a, b) => sideLayers.indexOf(a.layer) - sideLayers.indexOf(b.layer));
  const pageCount = Math.max(1, Math.ceil(filtered.length / 12));
  const page = Math.min(pageCount, Math.max(1, Math.floor(Number(params.get("dataset_page"))) || 1));
  const visibleQuestions = filtered.slice((page - 1) * 12, page * 12);
  const selectEdition = (key: string) => navigate({ snapshot: key, question: null, dataset_page: null });
  return <>
    <nav className={styles.detailTabs} aria-label="评估集分区">{[["questions", "题目"], ["history", "迭代记录"], ["tests", "关联测试"]].map(([key, label]) => <button key={key} aria-current={view === key ? "page" : undefined} onClick={() => navigate({ dataset_view: key === "questions" ? null : key })}>{label}</button>)}</nav>
    {!selected ? <p className={styles.empty}>尚无评估集。生成评估产物后可在这里查看。</p> : <>
      <div className={styles.sectionHead}><h2>{manifest ? datasetTitle(manifest) : currentQuery.data ? "评估集信息未记录" : "评估集"}</h2><label className={styles.catalogSelect}><span className="sr-only">题目版本</span><select aria-label="题目版本" value={selected.key} onChange={event => selectEdition(event.target.value)}>{groups.map((group, index) => <option key={group.key} value={group.key}>{index === 0 ? "最近使用 · " : ""}{reqV2Time(group.firstSeen)}</option>)}</select></label></div>
      {currentQuery.isPending && <p className={styles.empty} role="status">正在读取评估集…</p>}
      {currentQuery.error && <div className={styles.empty} role="alert"><p>{currentQuery.error.message}</p><Button variant="outline" onClick={() => void currentQuery.refetch()}>重新读取评估集</Button></div>}
      {currentQuery.data && !manifest && <p className={styles.empty}>评估集信息未记录，无法确认题量和覆盖范围。</p>}
      {currentQuery.data?.evidence.status !== "complete" && currentQuery.data && <p className={styles.notice}>来源运行证据{currentQuery.data.evidence.status === "invalid" ? "无效" : "不完整"}，以下仅供核对保存记录。</p>}
      {manifest && view === "questions" && <section aria-label="题目浏览">
        <div className={styles.datasetStats} aria-label="题库规模"><strong>{questionFiles(manifest).reduce((sum, file) => sum + file.cases, 0)}<span>道评估题</span></strong><span>{questionFiles(manifest).length} 个分类</span><span>用于 {selected.items.filter(item => includeArchived || !item.superseded).length} 次测试</span></div>
        <div className={styles.filters} aria-label="评估对象">{[["screening", "Screening · 需求理解", ["extraction", "conversations", "reducer", "readiness"]], ["builder", "Builder · 选配准入", ["policy", "ui-contract"]]].map(([key, label, layers]) => <button key={key as string} aria-pressed={side === key} onClick={() => navigate({ dataset_side: key as string, dataset_layer: null, question: null, dataset_page: null })}>{label as string}<span>{manifest.files.filter(file => (layers as string[]).includes(file.layer)).reduce((sum, file) => sum + file.cases, 0)} 题</span></button>)}</div>
        <div className={styles.questionToolbar}><div className={styles.filters} aria-label="题目分类"><button aria-pressed={layer === "all"} onClick={() => navigate({ dataset_layer: null, question: null, dataset_page: null })}>全部分类</button>{sideLayers.map(key => <button key={key} aria-pressed={layer === key} onClick={() => navigate({ dataset_layer: key, question: null, dataset_page: null })}>{topicName(key)}<span>{manifest.files.find(file => file.layer === key)?.cases ?? "—"}</span></button>)}</div><label className={styles.search}><Search size={16} aria-hidden /><span className="sr-only">搜索评估题目</span><input value={search} onChange={event => setSearch(event.target.value)} placeholder="搜索场景、输入或题号" /></label></div>
        {questions.isPending && <p role="status">正在读取题目…</p>}
        {questions.error && <p role="alert">题目读取失败。<Button variant="outline" onClick={() => void questions.refetch()}>重新读取题目</Button></p>}
        {questions.data?.notes.map(note => <p role="alert" className={styles.notice} key={note}>{note}</p>)}
        {questions.data && <><div className={styles.catalogHeading}><h3>{side === "builder" ? "Builder · 选配准入" : "Screening · 需求理解"}</h3><span>{filtered.length} 题</span></div><div>{sideLayers.map(groupLayer => {
          const rows = visibleQuestions.filter(question => question.layer === groupLayer);
          if (!rows.length) return null;
          return <section key={groupLayer} aria-label={`${topicName(groupLayer)}题目`} className={styles.questionGroup}><div className={styles.questionGroupTitle}><h4>{topicName(groupLayer)}</h4><p>{topics[groupLayer]?.purpose}</p></div>{rows.map(question => {
          const key = questionKey(question), expanded = params.get("question") === key;
          return <article key={key} className={styles.questionRow}><button aria-expanded={expanded} aria-controls={`question-${question.id}`} onClick={() => navigate({ question: expanded ? null : key })}><b>{question.title || question.id}</b><small title={splitHelp[question.split ?? ""]}>用途：{splitLabels[question.split ?? ""]?.name ?? question.split ?? "未记录"}</small><ChevronDown size={16} aria-hidden /></button>{expanded && <div id={`question-${question.id}`}><QuestionDesign question={question} /></div>}</article>;
          })}</section>;
        })}</div>{!filtered.length && <p className={styles.empty}>{questions.data.notes.length ? "该范围的题目暂不可读取。" : "没有符合条件的题目。"}</p>}{pageCount > 1 && <nav className={styles.pagination} aria-label="题目分页"><span>第 {page} / {pageCount} 页</span><Button variant="outline" disabled={page === 1} onClick={() => navigate({ dataset_page: String(page - 1), question: null })}>上一页</Button><Button variant="outline" disabled={page === pageCount} onClick={() => navigate({ dataset_page: String(page + 1), question: null })}>下一页</Button></nav>}</>}
        <details className={styles.versionDetails}><summary>题库分组与评分信息</summary><DatasetCoverage manifest={manifest} /></details>
      </section>}
      {manifest && view === "history" && <div className={styles.datasetLayout}>
        <nav className={styles.snapshotList} aria-label="选择迭代记录"><label className={styles.checkbox}><input type="checkbox" checked={includeArchived} onChange={event => navigate({ dataset_archived: event.target.checked ? "true" : null })} />包含归档</label>{groups.map((group, index) => <button key={group.key} aria-current={group.key === selected.key ? "page" : undefined} onClick={() => selectEdition(group.key)}><b><BookOpen size={15} aria-hidden />{index === 0 ? "最近使用" : editionLabel(group.firstSeen)}</b><span>{reqV2Time(group.firstSeen)}</span><small>{group.items.filter(item => includeArchived || !item.superseded).length} 次测试</small></button>)}</nav>
        <section className={styles.datasetContent} aria-label="内容变化"><h3>题目变化</h3>{!previous ? <p className={styles.muted}>最早使用的记录，没有更早题目可比较。</p> : <>
          <p className={styles.catalogHelp}>与 {reqV2Time(previous.firstSeen)} 的保存内容比较</p>
          {(previousQuery.isPending || questions.isPending || olderQuestions.isPending) && <p role="status">正在核对题目变化…</p>}
          {(previousQuery.error || questions.error || olderQuestions.error) && <p role="alert">内容变化读取失败。<Button variant="outline" onClick={() => { void previousQuery.refetch(); void questions.refetch(); void olderQuestions.refetch(); }}>重试核对</Button></p>}
          {questions.data && olderQuestions.data && (questions.data.notes.length || olderQuestions.data.notes.length ? <p className={styles.notice}>题目文件校验不完整，暂不能确认逐题增删。</p> : <QuestionChanges current={questions.data.cases} previous={olderQuestions.data.cases} />)}
          {previousQuery.data?.manifest && <details className={styles.versionDetails}><summary>分类与评分规则变化</summary><ul className={styles.changeList}>{manifestChanges(manifest, previousQuery.data.manifest).map(change => <li key={change}>{change}</li>)}</ul></details>}
        </>}<details className={styles.versionDetails}><summary>版本核对信息</summary><p>首次使用 {reqV2Time(selected.firstSeen)}</p><p>内容标识 {run.manifest_sha256 || "未记录"}</p><p>按首次使用时间排列；修改原因未单独记录。</p></details></section>
      </div>}
      {view === "tests" && <section aria-label="关联测试"><div className={styles.catalogHeading}><h3>使用此评估集的测试</h3><label className={styles.checkbox}><input type="checkbox" checked={includeArchived} onChange={event => navigate({ dataset_archived: event.target.checked ? "true" : null })} />包含归档</label></div>{selected.items.filter(item => includeArchived || !item.superseded).map(item => <button key={item.id} className={styles.datasetTest} onClick={() => openRun(item.id, "results")}><span><b>{item.models.map(model => model.model).join("、") || reqV2ModeLabels[item.mode] || item.mode}</b><small>{reqV2Time(item.created_at)} · {reqV2ModeLabels[item.mode] ?? item.mode}</small></span><span className={item.gate_passed == null ? "status-unknown" : item.gate_passed ? "status-pass" : "status-fail"}>{item.gate_passed == null ? "门槛未记录" : item.gate_passed ? "门槛通过" : "门槛未通过"}</span><ArrowRight size={16} aria-hidden /></button>)}</section>}
    </>}
  </>;
}
