import Link from "next/link";
import { Download } from "lucide-react";
import { HealthStatus } from "./health-status";
import { Button } from "./ui/button";

export function AppHeader({ title, exportHref }: { title?: string; exportHref?: string }) {
  return (
    <header className="flex h-14 shrink-0 items-center justify-between gap-2 border-b bg-[var(--surface-1)] px-3 sm:px-6">
      <div className="flex min-w-0 items-center gap-3">
        <Link href="/" className="shrink-0 whitespace-nowrap font-semibold tracking-[-0.2px]"><span className="sm:hidden">装机配置单</span><span className="hidden sm:inline">装机配置单 Agent</span></Link>
        {title && <span className="hidden min-w-0 items-center gap-3 sm:flex"><span className="text-[var(--ink-subtle)]">/</span><span className="truncate text-sm text-[var(--ink-muted)]">{title}</span></span>}
      </div>
      <div className="flex items-center gap-3">
        <HealthStatus />
        {exportHref && <Button asChild variant="outline" size="sm"><a href={exportHref} aria-label="导出 Markdown"><Download size={15} /><span className="hidden sm:inline">导出 Markdown</span></a></Button>}
      </div>
    </header>
  );
}
