"use client";

import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Archive, ArchiveRestore, MoreHorizontal, Pencil, Trash2 } from "lucide-react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { api } from "@/lib/api/client";
import { userMessage } from "@/lib/api/problem";
import { queryKeys } from "@/lib/api/query-keys";
import type { SessionSummary } from "@/lib/api/types";
import { Button } from "./ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "./ui/dialog";
import { Input } from "./ui/input";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "./ui/dropdown-menu";

export function SessionActions({ session, current, onLeave }: { session: SessionSummary; current: boolean; onLeave: () => void }) {
  const [dialog, setDialog] = useState<"rename" | "delete" | null>(null);
  const [title, setTitle] = useState(session.title);
  const client = useQueryClient();
  const router = useRouter();
  const mutation = useMutation({
    mutationFn: async (action: "rename" | "archive" | "delete") => {
      if (action === "delete") { await api.deleteSession(session.id); return null; }
      return api.updateSession(session.id, action === "rename" ? { title: title.trim() } : { archived: !session.archived });
    },
    onSuccess: (updated, action) => {
      setDialog(null);
      if (current && (action === "delete" || action === "archive")) {
        onLeave();
        router.replace("/");
      }
      if (updated) client.setQueryData(queryKeys.session(session.id), updated);
      else client.removeQueries({ predicate: (query) => query.queryKey[1] === session.id });
      void client.invalidateQueries({ queryKey: queryKeys.sessions });
      toast.success(action === "rename" ? "对话已重命名" : action === "delete" ? "对话已删除" : session.archived ? "对话已恢复" : "对话已归档，可在已归档列表中恢复");
    },
    onError: (error, action) => { if (action === "archive") toast.error(userMessage(error)); },
  });
  const open = (kind: "rename" | "delete") => { mutation.reset(); setTitle(session.title); setDialog(kind); };
  return <>
    <DropdownMenu>
      <DropdownMenuTrigger asChild><Button variant="ghost" size="icon-sm" className="absolute top-2 right-1 h-8 w-8 text-[var(--ink-muted)] [@media(pointer:coarse)]:h-11 [@media(pointer:coarse)]:w-11" disabled={mutation.isPending} aria-label={`${session.title || "新对话"}的对话菜单`} title="对话菜单"><MoreHorizontal size={16} /></Button></DropdownMenuTrigger>
      <DropdownMenuContent align="start" side="right" onCloseAutoFocus={(event) => { if (dialog) event.preventDefault(); }}>
        <DropdownMenuItem className="[@media(pointer:coarse)]:min-h-11" onSelect={() => open("rename")}><Pencil size={14} />重命名</DropdownMenuItem>
        <DropdownMenuItem className="[@media(pointer:coarse)]:min-h-11" onSelect={() => mutation.mutate("archive")}>{session.archived ? <ArchiveRestore size={14} /> : <Archive size={14} />}{session.archived ? "取消归档" : "归档对话"}</DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem className="[@media(pointer:coarse)]:min-h-11" variant="destructive" onSelect={() => open("delete")}><Trash2 size={14} />删除对话</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
    <Dialog open={dialog !== null} onOpenChange={(value) => { if (!value && !mutation.isPending) setDialog(null); }}>
      <DialogContent className="sm:max-w-md">
        <DialogTitle>{dialog === "rename" ? "重命名对话" : "删除对话？"}</DialogTitle>
        <DialogDescription>{dialog === "rename" ? "设置一个方便识别的标题，最多 80 个字符。" : `将永久删除「${session.title || "新对话"}」的消息、需求和全部配置版本，分享链接也会失效。此操作无法撤销。`}</DialogDescription>
        <form className="space-y-4" onSubmit={(event) => { event.preventDefault(); if (dialog) mutation.mutate(dialog); }}>
          {dialog === "rename" && <div><label htmlFor={`title-${session.id}`} className="mb-2 block text-sm">对话标题</label><Input className="min-h-11" id={`title-${session.id}`} value={title} onChange={(event) => setTitle(event.target.value)} maxLength={80} autoComplete="off" disabled={mutation.isPending} /></div>}
          {mutation.isError && <p role="alert" className="text-sm status-fail">{userMessage(mutation.error)} 请重试，或取消后稍后处理。</p>}
          <div className="flex justify-end gap-2">
            <Button className="min-h-11" type="button" variant="outline" disabled={mutation.isPending} onClick={() => setDialog(null)}>取消</Button>
            <Button className="min-h-11" type="submit" variant={dialog === "delete" ? "destructive" : "default"} disabled={mutation.isPending || (dialog === "rename" && !title.trim())}>{mutation.isPending ? "正在保存…" : dialog === "rename" ? "保存标题" : "确认删除"}</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  </>;
}
