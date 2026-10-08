"use client";

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { reqv2, reqV2Time, type ReqV2CurrentPrompts, type ReqV2RunSummary, type ReqV2PromptReview } from "@/lib/evaldesk/reqv2";
import { promptDiff, promptLength, type PromptChange } from "@/lib/evaldesk/prompt-diff";
import styles from "./workbench.module.css";

export function promptComponentLabel(name: string) {
  if (name === "system") return "主提示词";
  if (name === "format_retry") return "格式纠偏";
  return name.startsWith("output_contract_attempt_") ? `第 ${name.replace("output_contract_attempt_", "")} 次尝试输出契约` : name;
}

function sectionLabel(text: string, index: number) {
  const known = [
    ["你负责本轮需求语义解析", "职责与边界"], ["逐项理解本轮信息", "语义判断与证据"],
    ["操作语义", "需求操作规则"], ["turn_signals", "本轮请求信号"],
    ["proposals", "建议值规则"], ["answer", "回答规则"], ["字段与值", "字段与取值"], ["输出契约", "输出格式与示例"],
  ];
  return known.find(([prefix]) => text.startsWith(prefix))?.[1] ?? `第 ${index + 1} 段 · ${text.split(/[\n：:。]/)[0].slice(0, 24)}`;
}

function DiffExcerpt({ parts, side, compact }: { parts: PromptChange[]; side: "added" | "removed"; compact: boolean }) {
  const visible = parts.filter(part => part.kind === "same" || part.kind === side);
  return <div className={styles.promptExcerpt}>{visible.map((part, index) => {
    if (part.kind !== "same") return <mark key={index} data-change={part.kind}>{part.text}</mark>;
    const chars = Array.from(part.text);
    if (!compact || chars.length <= 140) return <span key={index}>{part.text}</span>;
    const head = index > 0 ? 60 : 0, tail = index < visible.length - 1 ? 60 : 0;
    return <div key={index} className={styles.contextExcerpt}>{chars.slice(0, head).join("")}<details className={styles.inlineContext}><summary>展开 {chars.length - head - tail} 字符上下文</summary><span>{chars.slice(head, chars.length - tail).join("")}</span></details>{tail ? chars.slice(-tail).join("") : ""}</div>;
  })}</div>;
}

interface Edition {
  id: string; source: "current" | "git" | "run_snapshot"; label: string; created_at?: string; commit?: string; subject?: string; runs?: string[];
  components: ReqV2CurrentPrompts["components"];
  review?: ReqV2PromptReview;
}

const verificationLabels = { live_record: "有真实模型记录", offline_record: "有离线回放记录", tests_added: "有回归测试，未确认执行结果", not_recorded: "改后验证未记录" };

function PromptReview({ review, runs, openRun, navigate, showHeading = true }: { review?: ReqV2PromptReview; runs: ReqV2RunSummary[]; openRun: (id: string) => void; navigate: (updates: Record<string, string | null>) => void; showHeading?: boolean }) {
  if (!review) return <p className={styles.catalogHelp}>此版本的改动原因与验证结果尚未整理，不从提交标题或相邻运行推断效果。</p>;
  const runNames = review.run_names ?? [];
  const referencedRuns = runNames.map(name => runs.find(run => run.dir_name === name));
  return <section className={styles.promptReview} aria-label="改动原因与验证">
    {showHeading && <div className={styles.catalogHeading}><h3>{review.title}</h3><span>{verificationLabels[review.verification.kind]}</span></div>}
    <dl><div><dt>改动原因</dt><dd>{review.reason}</dd></div><div><dt>具体调整</dt><dd>{review.changes}</dd></div><div><dt>验证与效果</dt><dd>{review.verification.summary}</dd></div><div><dt>结论边界</dt><dd>{review.verification.limitation}</dd></div></dl>
    {referencedRuns.length > 0 && <div className={styles.reviewRuns}><p className={styles.catalogHelp}>记录提及的测试 · 旧运行未保存提示词原文</p>{runNames.map((name, index) => {
      const run = referencedRuns[index];
      return run ? <Button key={name} variant="outline" onClick={() => openRun(run.id)}>{name} · {run.gate_passed == null ? "门槛未知" : run.gate_passed ? "门槛通过" : "门槛未通过"}</Button> : <p key={name} className={styles.catalogHelp}>{name} · 当前产物未找到</p>;
    })}{referencedRuns.length === 2 && referencedRuns.every(Boolean) && <Button variant="outline" onClick={() => navigate({ area: "compare", run: null, view: null, panel: null, case: null, layer: null, va: referencedRuns[0]!.id, vb: referencedRuns[1]!.id })}>对比这两次测试</Button>}</div>}
    <details className={styles.versionDetails}><summary>查看溯源依据</summary>{(review.evidence ?? []).map((source, index) => <div key={index} className={styles.reviewEvidence}><b>{source.kind === "commit" ? "提交说明" : source.kind === "test" ? "回归测试" : "验证文档"}</b><p className={styles.catalogHelp}>{source.reference}</p><pre>{source.excerpt}</pre></div>)}</details>
  </section>;
}

export function PromptVersionsView({ role, params, navigate, runs, openRun }: {
  role: string; params: URLSearchParams; navigate: (updates: Record<string, string | null>) => void;
  runs: ReqV2RunSummary[]; openRun: (id: string, panel?: string) => void;
}) {
  const current = useQuery({ queryKey: ["evaldesk", "reqv2", "prompts"], queryFn: reqv2.prompts, retry: false, refetchOnWindowFocus: false });
  const history = useQuery({ queryKey: ["evaldesk", "reqv2", "prompt-versions"], queryFn: reqv2.promptVersions, retry: false, refetchOnWindowFocus: false });
  const [onlyChanges, setOnlyChanges] = useState(true);
  const editions: Edition[] = [
    ...(current.data ? [{ id: "current", source: "current" as const, label: "当前编译版本", components: current.data.components.filter(component => component.role === role) }] : []),
    ...(history.data?.versions.filter(version => version.role === role).map(version => ({ ...version, label: `${reqV2Time(version.created_at)} · ${version.review?.title ?? (version.source === "git" ? "Git 历史" : "测试保存")}` })) ?? []),
  ];
  const selectedId = params.get("prompt_version") ?? "current";
  const edition = editions.find(version => version.id === selectedId);
  const components = edition?.components ?? [];
  const componentName = params.get("prompt_component") ?? "system";
  const selected = components.find(component => component.name === componentName);
  const compare = params.get("prompt_view") === "compare";
  const baselineId = params.get("prompt_base") ?? editions.slice(editions.findIndex(version => version.id === selectedId) + 1).find(version => version.components.some(component => component.name === componentName && component.sha256 !== selected?.sha256))?.id ?? "";
  const baseline = editions.find(version => version.id === baselineId);
  const previous = baseline?.components.find(component => component.name === componentName);
  const beforeText = previous?.text, afterText = selected?.text;
  const blocks = compare && beforeText !== undefined && afterText !== undefined ? promptDiff(beforeText, afterText) : [];
  const changes = blocks.flatMap(block => block.parts);
  const added = changes.filter(part => part.kind === "added").reduce((sum, part) => sum + promptLength(part.text), 0);
  const removed = changes.filter(part => part.kind === "removed").reduce((sum, part) => sum + promptLength(part.text), 0);
  const paragraphs = selected?.text.split(/\n\s*\n/) ?? [];
  const segmented = paragraphs.length > 1 && promptLength(selected?.text ?? "") > 1800;
  const full = !segmented || params.get("prompt_read") === "full";
  const sectionIndex = Math.max(0, Math.min(paragraphs.length - 1, Math.trunc(Number(params.get("prompt_section"))) || 0));
  const pending = selectedId === "current" ? current.isPending : history.isPending;
  const sourceLabel = (version: Edition) => version.source === "current" ? "当前编译版本" : version.source === "git" ? "Git 历史原文" : "测试保存的原文 · 指纹校验通过";
  const chooseEdition = (id: string) => navigate({ prompt_version: id === "current" ? null : id, prompt_component: null, prompt_base: null, prompt_section: null });
  const reviewEdition = edition?.review ? edition : edition?.source === "current" ? editions.find(version => version.source === "git" && version.review && selected && version.components.some(component => component.name === selected.name && component.sha256 === selected.sha256)) : undefined;
  const feedback = <>
    {(current.error || history.error) && <div className={styles.notice} role="alert"><p>{current.error ? "无法读取当前提示词原文。" : history.error?.message}</p><Button variant="outline" onClick={() => { void current.refetch(); void history.refetch(); }}>重新读取提示词</Button></div>}
    {history.isPending && <p className={styles.catalogHelp} role="status">正在读取历史提示词版本…</p>}
    {history.data?.notes.map((note, index) => <p className={styles.notice} key={index}>{note}</p>)}
  </>;
  if (params.get("prompt_view") === "iterations") return <section aria-label="提示词迭代记录">
    {feedback}
    <div className={styles.catalogHeading}><h2>内容迭代 <span>{editions.filter(version => version.source === "git").length}</span></h2><span>按内容首次出现时间排序</span></div>
    <p className={styles.catalogHelp}>原因依据提交和原文整理；验证记录分别标注真实模型、离线回放和仅有测试代码，不将它们合成效果提升。</p>
    {editions.filter(version => version.source === "git").map(version => <details key={version.id} className={styles.iterationRow}>
      <summary><span><b>{version.review?.title ?? "改动说明待整理"}</b><small>{reqV2Time(version.created_at ?? null)}</small></span><small>{version.review ? verificationLabels[version.review.verification.kind] : "暂无验证结论"}</small></summary>
      <PromptReview review={version.review} runs={runs} openRun={openRun} navigate={navigate} showHeading={false} />
      <div className={styles.selection}><Button variant="outline" onClick={() => navigate({ prompt_view: null, prompt_version: version.id, prompt_base: null, prompt_section: null })}>查看本版原文</Button><Button variant="outline" onClick={() => navigate({ prompt_view: "compare", prompt_version: version.id, prompt_base: null, prompt_section: null })}>查看本次 diff</Button></div>
    </details>)}
    {!history.isPending && !history.error && !editions.some(version => version.source === "git") && <p className={styles.empty}>未找到可读取的内容迭代，请在「版本与原文」查看当前原文或测试保存版本。</p>}
  </section>;
  return <section aria-label="提示词版本浏览">
    <div className={styles.selection}>
      <label className={styles.selectLabel}>提示词版本<select aria-label="提示词版本" value={selectedId} onChange={event => chooseEdition(event.target.value)}>{!current.data && <option value="current" disabled>当前版本暂不可读取</option>}{editions.map(version => <option key={version.id} value={version.id}>{version.label}</option>)}</select><small>{history.data ? `${editions.filter(version => version.source !== "current").length} 个历史内容版本` : history.isPending ? "正在读取版本列表" : "历史版本暂不可用"}</small></label>
      {compare && <label className={styles.selectLabel}>对照版本<select aria-label="对照版本" value={baselineId} onChange={event => navigate({ prompt_base: event.target.value })}><option value="">选择对照版本</option>{editions.filter(version => version.id !== selectedId).map(version => <option key={version.id} value={version.id}>{version.label}</option>)}</select></label>}
      {!compare && <Button variant="outline" disabled={!selected || !baseline} onClick={() => navigate({ prompt_view: "compare", prompt_base: baselineId })}>对比旧版</Button>}
    </div>
    {feedback}
    {pending ? <p className={styles.empty} role="status">正在读取提示词原文…</p> : !edition ? <p className={styles.empty}>所选版本不存在或已无法读取，请选择其他版本。</p> : <>
      <div className={styles.scopeNote}><b>{sourceLabel(edition)}</b><p>{edition.source === "current" ? "取自本机评估服务；修改源码后需重启服务更新。" : edition.source === "git" ? "从该提交的字符串常量读取；不代表历史测试已保存完整请求。" : "取自运行前保存的静态提示词；动态用户输入与会话上下文另行组装。"}</p></div>
      <details className={styles.versionDetails}><summary>改动说明 · {reviewEdition?.review?.title ?? "原因与验证待整理"}</summary>{reviewEdition && reviewEdition.id !== edition.id && <p className={styles.catalogHelp}>所选组成部分与 {reviewEdition.label} 原文一致；以下说明针对该历史版本，可能涉及其他组件与守卫，不代表当前工作区再次验证。</p>}<PromptReview review={reviewEdition?.review} runs={runs} openRun={openRun} navigate={navigate} /></details>
      <div className={styles.filters} aria-label="提示词组成">{[...components].sort((a, b) => a.name === "system" ? -1 : b.name === "system" ? 1 : a.name.localeCompare(b.name)).map(component => <button key={component.name} aria-pressed={selected?.name === component.name} onClick={() => navigate({ prompt_component: component.name, prompt_section: null })}>{promptComponentLabel(component.name)}</button>)}</div>
      {!selected ? <p className={styles.empty}>此版本没有保存这一组成部分，请选择上方可用的原文。</p> : compare ? <section aria-label="提示词内容对比">
        <div className={styles.catalogHeading}><h2>{promptComponentLabel(selected.name)}变化</h2><label className={styles.checkbox}><input type="checkbox" checked={onlyChanges} onChange={event => setOnlyChanges(event.target.checked)} />只看改动</label></div>
        <p className={styles.muted}>{baseline?.label ?? "未选对照版本"} → {edition.label}</p>
        {!previous ? <p className={styles.empty}>对照版本未保存这一组成部分，无法比较；不将缺失视为新增。</p> : previous.text === selected.text ? <p className={styles.empty}>两版原文一致，没有内容变化。</p> : <>
          <div className={styles.diffStats} aria-label="改动统计"><b>+{added.toLocaleString()} 新增字符</b><b>−{removed.toLocaleString()} 移除字符</b><span>{blocks.filter(block => block.kind === "changed").length} 处改动</span><span>{promptLength(previous.text).toLocaleString()} → {promptLength(selected.text).toLocaleString()} 字符</span></div>
          {blocks.some(block => !block.detailed) && <p className={styles.notice}>大幅变化的区间按整段替换展示，其字符数包含未细化内容；其余区间仍逐字标记。</p>}
          <ol className={styles.promptDiff}>{blocks.map((block, index) => block.kind === "same" ? <li key={index} data-change="same"><details open={!onlyChanges} key={`${index}-${onlyChanges}`}><summary>未改动 · 原文第 {block.beforeLine} 行起</summary><pre>{block.before}</pre></details></li> : <li key={index} data-change="changed">
            <div className={styles.diffLocation}>原文第 {block.beforeLine} → {block.afterLine} 行{!block.detailed && " · 整段替换"}</div>
            <div className={styles.diffPair}>
              {block.parts.some(part => part.kind === "removed") && <div data-change="removed"><h3>− 移除</h3><DiffExcerpt parts={block.parts} side="removed" compact={onlyChanges} /></div>}
              {block.parts.some(part => part.kind === "added") && <div data-change="added"><h3>+ 新增</h3><DiffExcerpt parts={block.parts} side="added" compact={onlyChanges} /></div>}
            </div>
          </li>)}</ol>
        </>}
      </section> : <section aria-label="当前提示词原文">
        <div className={styles.catalogHeading}><h2>{promptComponentLabel(selected.name)}</h2><span>{promptLength(selected.text).toLocaleString()} 字符{segmented && ` · ${paragraphs.length} 个段落`}</span></div>
        {segmented && <>
          <div className={styles.filters} aria-label="原文阅读方式"><button aria-pressed={!full} onClick={() => navigate({ prompt_read: null })}>分段阅读</button><button aria-pressed={full} onClick={() => navigate({ prompt_read: "full" })}>完整原文</button></div>
          {!full && <div className={styles.readingLayout}>
            <nav aria-label="原文段落">{paragraphs.map((paragraph, index) => <button key={index} aria-current={sectionIndex === index ? "true" : undefined} onClick={() => navigate({ prompt_section: index ? String(index) : null })}><span>{sectionLabel(paragraph, index)}</span><small>{promptLength(paragraph).toLocaleString()} 字符</small></button>)}</nav>
            <section aria-label="所选原文段落"><h3>{sectionLabel(paragraphs[sectionIndex], sectionIndex)}</h3><pre className={styles.promptText} tabIndex={0}>{paragraphs[sectionIndex]}</pre></section>
          </div>}
        </>}
        {full && <pre className={styles.promptText} tabIndex={0} aria-label={`${role === "screening" ? "Screening" : "Builder"} ${promptComponentLabel(selected.name)}原文`}>{selected.text}</pre>}
      </section>}
      {!!edition.runs?.length && <section className={styles.turn} aria-label="此版本的关联测试"><h3>使用此版本的测试</h3>{edition.runs.map(id => <Button variant="outline" key={id} onClick={() => openRun(id, "prompts")}>{runs.find(run => run.id === id)?.dir_name ?? "查看运行"}</Button>)}</section>}
      <details className={styles.versionDetails}><summary>版本来源与内容校验</summary>{edition.commit && <p>Git 提交：{edition.commit}</p>}{edition.subject && <p>{edition.subject}</p>}{selected && <p>原文 SHA256：{selected.sha256}</p>}<p>Git 历史只读取主提示词及静态纠偏文本；旧版 Builder 输出契约未执行还原。每次请求的动态上下文不包含在静态原文中。</p></details>
    </>}
  </section>;
}
