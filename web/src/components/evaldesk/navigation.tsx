"use client";

import { BookOpen, GitCommitHorizontal, History, ListChecks, Menu, Rows3, X } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import type { RunSummary } from "@/lib/evaldesk/types";
import { getSuiteVersions } from "@/lib/evaldesk/catalog";
import styles from "./navigation.module.css";

export type DeskSection = "runs" | "compare" | "commits" | "suites" | "timeline";
const entries = [
  { key: "runs", label: "评估运行", icon: ListChecks },
  { key: "compare", label: "运行对比", icon: Rows3 },
  { key: "commits", label: "代码提交记录", icon: GitCommitHorizontal },
  { key: "suites", label: "题库与评估集", icon: BookOpen },
  { key: "timeline", label: "溯源时间线", icon: History },
] as const;

export function WorkbenchNavigation({ active, version, runs, navigate }: { active: DeskSection; version: string; runs: RunSummary[]; navigate: (section: DeskSection, version?: string) => void }) {
  const [expanded, setExpanded] = useState(false);
  const versions = getSuiteVersions(runs);
  const label = entries.find(e => e.key === active)!.label;
  const select = (section: DeskSection, selectedVersion?: string) => {
    navigate(section, selectedVersion);
    setExpanded(false);
    requestAnimationFrame(() => document.getElementById("eval-page-heading")?.focus({ preventScroll: true }));
  };
  return <>
    <div className={styles.mobileBar}><Button variant="outline" aria-expanded={expanded} aria-controls="evaldesk-navigation" onClick={() => setExpanded(!expanded)}>{expanded ? <X size={16} /> : <Menu size={16} />}工作台菜单</Button><span>{label}</span></div>
    <aside className={`${styles.sidebar} ${expanded ? styles.expanded : ""}`} id="evaldesk-navigation">
      <p className={styles.caption}>评估工作台</p>
      <nav aria-label="工作台导航">{entries.map(({ key, label: text, icon: Icon }, index) => <div key={key}>
        {index === 2 && <p className={styles.groupLabel}>资料溯源</p>}
        <button className={styles.navItem} aria-current={active === key ? "page" : undefined} onClick={() => select(key)}><Icon size={17} aria-hidden="true" /><span>{text}</span></button>
        {key === "suites" && active === "suites" && <div className={styles.versions} role="group" aria-label="题库版本">{versions.map(item => <button key={item} aria-label={`题库版本 ${item}`} aria-current={version === item ? "page" : undefined} onClick={() => select("suites", item)}>{item}</button>)}{!versions.length && <p className={styles.hint}>暂无已记录版本</p>}</div>}
      </div>)}</nav>
      <p className={styles.hint}>只读资料 · {runs.length} 次历史运行<br />查看资料不会自动更换对照组。</p>
    </aside>
  </>;
}
