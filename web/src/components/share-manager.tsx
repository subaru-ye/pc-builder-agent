"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, Check, Copy, Download, ImageDown, Link2, Loader2, RefreshCw, Share2, Trash2 } from "lucide-react";
import { useRef, useState } from "react";
import { toast } from "sonner";
import { api, publicExportURL } from "@/lib/api/client";
import { queryKeys } from "@/lib/api/query-keys";
import type { Share } from "@/lib/api/types";
import { userMessage } from "@/lib/api/problem";
import { useMobile } from "@/hooks/use-mobile";
import { Button } from "./ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from "./ui/dialog";
import { Drawer, DrawerContent, DrawerDescription, DrawerHeader, DrawerTitle, DrawerTrigger } from "./ui/drawer";

export function ShareManager({ sessionID, version }: { sessionID: string; version: number }) {

  return <ShareManagerInstance key={`${sessionID}:${version}`} sessionID={sessionID} version={version} />;
}

function ShareManagerInstance({ sessionID, version }: { sessionID: string; version: number }) {
  const mobile = useMobile();
  const [open, setOpen] = useState(false);
  const trigger = <Button variant="outline" size="sm" className="min-h-11 sm:min-h-0" aria-label={`分享配置 v${version}`}><Share2 size={15} /><span className="hidden sm:inline">分享</span></Button>;
  const content = <ShareContent sessionID={sessionID} version={version} open={open} />;
  if (mobile) {
    return <Drawer open={open} onOpenChange={setOpen}><DrawerTrigger asChild>{trigger}</DrawerTrigger><DrawerContent><DrawerHeader className="text-left"><DrawerTitle>分享配置 v{version}</DrawerTitle><DrawerDescription>链接固定到当前版本，不会随之后的改单变化。</DrawerDescription></DrawerHeader>{content}</DrawerContent></Drawer>;
  }
  return <Dialog open={open} onOpenChange={setOpen}><DialogTrigger asChild>{trigger}</DialogTrigger><DialogContent className="max-h-[84vh] max-w-lg overflow-y-auto border bg-[var(--surface-2)] p-0"><DialogHeader className="border-b px-6 py-5"><DialogTitle>分享配置 v{version}</DialogTitle><DialogDescription>链接固定到当前版本，不会随之后的改单变化。</DialogDescription></DialogHeader>{content}</DialogContent></Dialog>;
}

function ShareContent({ sessionID, version, open }: { sessionID: string; version: number; open: boolean }) {
  const client = useQueryClient();
  const [current, setCurrent] = useState<Share | null>(null);
  const [copied, setCopied] = useState(false);
  const createKey = useRef<string | null>(null);
  const shares = useQuery({
    queryKey: queryKeys.shares(sessionID, version),
    queryFn: () => api.listShares(sessionID, version),
    enabled: open,
  });
  const refresh = () => client.invalidateQueries({ queryKey: queryKeys.shares(sessionID, version) });
  const create = useMutation({
    mutationFn: () => {
      createKey.current ??= crypto.randomUUID();
      return api.createShare(sessionID, version, createKey.current);
    },
    retry: 2,
    onSuccess: (share) => { setCurrent(share); void refresh(); toast.success("分享链接已创建"); },
    onError: (error) => toast.error(userMessage(error)),
  });
  const revoke = useMutation({
    mutationFn: async ({ id, token }: { id: string; token?: string }) => {
      if (token) await api.revokeShareByToken(token, crypto.randomUUID());
      else await api.revokeShareByID(sessionID, version, id, crypto.randomUUID());
      return id;
    },
    onSuccess: (id) => {
      if (current?.id === id) setCurrent({ ...current, revoked_at: new Date().toISOString() });
      void refresh();
      toast.success("分享链接已撤销");
    },
    onError: (error) => toast.error(userMessage(error)),
  });
  const createNew = () => { createKey.current = crypto.randomUUID(); setCurrent(null); create.mutate(); };
  const copy = async () => {
    if (!current) return;
    await navigator.clipboard.writeText(current.url);
    setCopied(true);
    toast.success("链接已复制");
    window.setTimeout(() => setCopied(false), 1800);
  };
  const downloadImage = async () => {
    if (!current) return;
    try {
      const response = await fetch(`${current.url}/image`, { cache: "no-store" });
      if (!response.ok) throw new Error("image download failed");
      const href = URL.createObjectURL(await response.blob());
      const anchor = document.createElement("a");
      anchor.href = href; anchor.download = `pc-build-v${version}.png`; anchor.click();
      URL.revokeObjectURL(href);
    } catch { toast.error("分享图下载失败，请稍后重试"); }
  };
  return <div className="space-y-5 px-4 pb-5 sm:px-6">
    <div className="flex gap-3 border-l-2 border-l-[var(--review)] bg-[var(--surface-1)] p-3 text-sm">
      <AlertTriangle className="mt-0.5 shrink-0 status-review" size={16} />
      <p>任何拿到链接的人都可以查看这份配置。需求备注和会话内容不会被公开。</p>
    </div>
    {current && !current.revoked_at ? <div className="space-y-3 border-y py-4">
      <div className="flex items-center gap-2 text-sm status-pass"><Link2 size={16} />链接已就绪</div>
      <div className="flex gap-2"><input readOnly aria-label="分享链接" className="h-11 min-w-0 flex-1 rounded-md border bg-[var(--canvas)] px-3 font-mono text-xs" value={current.url} /><Button className="h-11" onClick={copy}>{copied ? <Check /> : <Copy />}{copied ? "已复制" : "复制"}</Button></div>
      {isLocalShareURL(current.url) && <p className="text-xs status-review">当前链接只在本机或可访问本机服务的网络内有效，尚未发布到公网。</p>}
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-3"><Button asChild variant="outline" className="h-11"><a href={publicExportURL(current.token)}><Download />Markdown</a></Button><Button variant="outline" className="h-11" onClick={downloadImage}><ImageDown />分享图</Button><Button variant="destructive" className="col-span-2 h-11 bg-[var(--error)] text-[var(--canvas)] hover:bg-[var(--error)]/90 sm:col-span-1" disabled={revoke.isPending} onClick={() => revoke.mutate({ id: current.id, token: current.token })}><Trash2 />撤销</Button></div>
    </div> : <Button className="h-11 w-full" disabled={create.isPending} onClick={() => create.mutate()}>{create.isPending ? <Loader2 className="animate-spin" /> : <Share2 />}创建当前版本分享</Button>}
    {current?.revoked_at && <div className="flex items-center justify-between gap-3 border-y py-4"><span className="text-sm text-[var(--ink-muted)]">刚才的链接已失效。</span><Button variant="outline" onClick={createNew}><RefreshCw />创建新链接</Button></div>}
    <section aria-labelledby="share-history-title"><div className="mb-2 flex items-center justify-between"><h3 id="share-history-title" className="font-medium">分享记录</h3>{shares.isFetching && <Loader2 className="animate-spin text-[var(--ink-subtle)]" size={15} />}</div>
      {shares.isError ? <p role="alert" className="text-sm status-fail">{userMessage(shares.error)}</p> : shares.data?.length ? <ol className="divide-y border-y">{shares.data.map((item) => <li key={item.id} className="flex min-h-14 items-center justify-between gap-3 py-3"><div><div className="text-sm">v{item.version} · {formatTime(item.created_at)}</div><div className={`mt-0.5 text-xs ${item.revoked_at ? "text-[var(--ink-subtle)]" : "status-pass"}`}>{item.revoked_at ? "已撤销" : "仍可访问 · 原链接未保存"}</div></div>{!item.revoked_at && <Button aria-label={`撤销 ${formatTime(item.created_at)} 的分享`} variant="ghost" size="icon" className="h-11 w-11" disabled={revoke.isPending} onClick={() => revoke.mutate({ id: item.id })}><Trash2 /></Button>}</li>)}</ol> : <p className="border-y py-5 text-sm text-[var(--ink-muted)]">当前版本还没有分享记录。</p>}
      <p className="mt-3 text-xs text-[var(--ink-subtle)]">出于安全考虑，旧链接内容不会保存；刷新后仍可撤销，需要复制时请创建新链接。</p>
    </section>
  </div>;
}

function formatTime(value: string) { return new Intl.DateTimeFormat("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" }).format(new Date(value)); }

export function isLocalShareURL(value: string) {
  try {
    const host = new URL(value).hostname.toLowerCase().replace(/^\[|\]$/g, "");
    if (host === "localhost" || host.endsWith(".localhost") || host === "::1") return true;
    const parts = host.split(".").map(Number);
    if (parts.length !== 4 || parts.some((part) => !Number.isInteger(part) || part < 0 || part > 255)) return host.startsWith("fc") || host.startsWith("fd") || host.startsWith("fe8") || host.startsWith("fe9") || host.startsWith("fea") || host.startsWith("feb");
    return parts[0] === 10 || parts[0] === 127 || (parts[0] === 172 && parts[1] >= 16 && parts[1] <= 31) || (parts[0] === 192 && parts[1] === 168) || (parts[0] === 169 && parts[1] === 254);
  } catch { return false; }
}
