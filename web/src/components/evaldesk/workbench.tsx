"use client";

import { useQueryClient } from "@tanstack/react-query";
import { FlaskConical } from "lucide-react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { useTheme, type ThemePreference } from "@/components/theme-provider";
import { ReqV2Workbench } from "./reqv2";
import styles from "./workbench.module.css";

// 只读评估工作台外壳：area 控制工作区，run/panel/case 控制证据深链。
// 旧的 desk/baseline/candidate 参数不恢复已移除的 legacy 桌。
export function EvalWorkbench() {
  const params = useSearchParams();
  const queryClient = useQueryClient();
  const { theme, setTheme } = useTheme();
  // 页面内筛选同步写入 URL，连续选择时始终保留上一次选择，并支持前进/后退。
  const navigate = (updates: Record<string, string | null>) => {
    const next = new URLSearchParams(window.location.search);
    Object.entries(updates).forEach(([key, value]) => value ? next.set(key, value) : next.delete(key));
    window.history.pushState(null, "", `/eval?${next}`);
    // 独立页面从标题开始；单题详情保留自身的证据定位，筛选也不跳回顶部。
    if (["area", "view", "run", "panel"].some(key => key in updates) && !next.get("case")) window.scrollTo(0, 0);
  };
  return <div className={styles.workspace}>
    <header className={styles.header}><Link href="/" className={styles.brand}><FlaskConical size={19} aria-hidden="true" /><span>装机配置单 <span className={styles.desktopOnly}>Agent</span></span></Link><label className={styles.theme}><span className="sr-only">外观</span><select aria-label="外观" value={theme} onChange={e => setTheme(e.target.value as ThemePreference)}><option value="system">系统</option><option value="dark">深色</option><option value="light">浅色</option></select></label></header>
    <ReqV2Workbench params={params} navigate={navigate} onReread={() => void queryClient.invalidateQueries({ queryKey: ["evaldesk"] })} />
  </div>;
}
