import { ArrowRight, Check, CircleHelp } from "lucide-react";
import type { ChangeSummary } from "@/lib/evaldesk/types";
import styles from "./workbench.module.css";

function Values({ baseline, candidate }: { baseline: string | null; candidate: string | null }) {
  return <div className={styles.readableValues}>
    <span><small>基线</small>{baseline ?? "未记录"}</span>
    <ArrowRight size={14} aria-hidden="true" />
    <span><small>候选</small>{candidate ?? "未记录"}</span>
  </div>;
}

/** 只呈现 Go 已核对的可读差异；哈希和原始条件留在版本证据中。 */
export function ReadableChanges({ changes }: { changes: ChangeSummary[] | undefined }) {
  if (!changes) return <p className={styles.notice}>服务尚未提供可读变化说明，请重启本机评估服务后重新读取。</p>;
  const changed = changes.filter(c => c.state === "changed");
  const unknown = changes.filter(c => c.state === "unknown");
  const same = changes.filter(c => c.state === "same");
  return <div aria-label="可读变化说明">
    {changed.length ? changed.map(change => <article key={change.key} data-testid={`change-${change.key}`} className={styles.readableChange}>
      <div className={styles.changeHeading}><h3>{change.label}</h3><span>已变化</span></div>
      <div className={styles.changeContent}>
        <p className={styles.changeLead}>{change.summary}</p>
        {(change.baseline != null || change.candidate != null) && <Values baseline={change.baseline} candidate={change.candidate} />}
        {change.details.length > 0 && <dl className={styles.changeDetails}>{change.details.map((detail, i) => <div key={i}><dt>{detail.label}</dt><dd><Values baseline={detail.baseline} candidate={detail.candidate} /></dd></div>)}</dl>}
        {change.notes.map((note, i) => <p className={styles.changeNote} key={i}><CircleHelp size={14} aria-hidden="true" /><span>{note}</span></p>)}
      </div>
    </article>) : <p className={styles.muted}>已记录条件中未发现变化。</p>}
    {same.length > 0 && <p className={styles.unchangedConditions}><Check size={14} aria-hidden="true" /><span>保持一致：{same.map(c => c.label).join("、")}。</span></p>}
    {unknown.length > 0 && <details className={styles.versionDetails}><summary>{unknown.length} 项条件无法完整判断</summary>{unknown.map(change => <div key={change.key} className={styles.conditionDetail}><b>{change.label}</b><p>{change.summary}</p>{change.notes.map((note, i) => <p key={i}>{note}</p>)}</div>)}</details>}
  </div>;
}
