import Link from "next/link";
import { Download } from "lucide-react";
import { HealthStatus } from "./health-status";
import { Button } from "./ui/button";
import { ShareManager } from "./share-manager";
import { LocalProfileMenu } from "./local-profile-menu";
import type { ReactNode } from "react";

export function AppHeader({ title, exportHref, share, navigation, showAccount = true, showEvaldesk = false }: { title?: string; exportHref?: string; share?: { sessionID: string; version: number }; navigation?: ReactNode; showAccount?: boolean; showEvaldesk?: boolean }) {
  return (
    <header className="flex h-14 shrink-0 items-center justify-between gap-2 border-b bg-[var(--surface-1)] px-3 sm:px-6">
      <div className="flex min-w-0 items-center gap-1 sm:gap-3">
        {navigation}
        <Link href="/" className="shrink-0 whitespace-nowrap font-semibold tracking-[-0.2px]"><span className="sm:hidden">装机配置单</span><span className="hidden sm:inline">装机配置单 Agent</span></Link>
        {title && <span className="hidden min-w-0 items-center gap-3 sm:flex"><span className="text-[var(--ink-subtle)]">/</span><span className="truncate text-sm text-[var(--ink-muted)]">{title}</span></span>}
      </div>
      <div className="flex shrink-0 items-center gap-1 sm:gap-3">
        {showEvaldesk && <Button asChild variant="ghost" className="min-h-11"><Link href="/eval">本机评估</Link></Button>}
        <HealthStatus />
        {share && <ShareManager sessionID={share.sessionID} version={share.version} />}
        {exportHref && <Button asChild variant="outline" size="sm" className="min-h-11 min-w-11 sm:min-h-0 sm:min-w-0"><a href={exportHref} aria-label="导出 Markdown"><Download size={15} /><span className="hidden sm:inline">导出 Markdown</span></a></Button>}
        {showAccount && !navigation && <LocalProfileMenu compact />}
      </div>
    </header>
  );
}
