"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronUp, LogIn, LogOut, Settings, UserPlus, UserRound } from "lucide-react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { api } from "@/lib/api/client";
import { queryKeys } from "@/lib/api/query-keys";
import { broadcastAuth } from "@/lib/auth-events";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "./ui/dropdown-menu";

export function LocalProfileMenu({ compact = false, forceLocal = false }: { compact?: boolean; forceLocal?: boolean }) {
  const router = useRouter();
  const queryClient = useQueryClient();
  const auth = useQuery({ queryKey: queryKeys.auth, queryFn: api.authState, enabled: !forceLocal });
  const state = forceLocal ? { enabled: false, authenticated: false, account: null } : auth.data;
  const logout = useMutation({
    mutationFn: api.logout,
    onSuccess: () => {
      queryClient.clear();
      broadcastAuth("logout");
      router.push("/");
      router.refresh();
    },
  });
  const name = state?.authenticated ? state.account?.display_name ?? "已登录用户" : "本地访客";
  const secondary = state?.authenticated ? state.account?.email : state?.enabled ? "可登录并同步会话" : "会话只属于当前浏览器身份";
  const label = state?.authenticated ? `打开 ${name} 的账号菜单` : "打开本地访客菜单";

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button type="button" aria-label={label} className={compact
          ? "flex h-10 min-w-10 items-center justify-center rounded-lg border bg-[var(--surface-2)] text-[var(--ink-muted)] outline-none hover:text-[var(--ink)] focus-visible:ring-2 focus-visible:ring-[var(--primary)]"
          : "group flex min-h-14 w-full items-center gap-3 rounded-lg px-2 text-left outline-none transition-colors hover:bg-[var(--surface-2)] focus-visible:ring-2 focus-visible:ring-[var(--primary)]"}>
          <span aria-hidden="true" className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg border bg-[var(--surface-2)] text-[var(--ink-muted)]"><UserRound size={17} /></span>
          {!compact && <><span className="min-w-0 flex-1"><span className="block truncate text-sm font-medium">{name}</span><span className="block truncate text-xs text-[var(--ink-subtle)]">{secondary}</span></span><ChevronUp aria-hidden="true" size={16} className="shrink-0 text-[var(--ink-subtle)] transition-transform group-data-[state=open]:rotate-180" /></>}
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent side={compact ? "bottom" : "top"} align="end" sideOffset={8} className="w-72 border border-[var(--hairline)] bg-[var(--surface-3)] p-1.5">
        <DropdownMenuLabel className="px-2 py-2">
          <span className="block truncate text-sm font-medium text-[var(--ink)]">{state?.authenticated ? name : state?.enabled ? "匿名使用中" : "本地匿名模式"}</span>
          <span className="mt-0.5 block truncate font-normal text-[var(--ink-subtle)]">{secondary}</span>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        {state?.authenticated ? <>
          <DropdownMenuItem asChild className="min-h-10 gap-3 px-2.5 py-2"><Link href="/account"><UserRound /><span>个人信息</span></Link></DropdownMenuItem>
          <DropdownMenuItem asChild className="min-h-10 gap-3 px-2.5 py-2"><Link href="/account#security"><Settings /><span>设置与安全</span></Link></DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem disabled={logout.isPending} onSelect={() => logout.mutate()} className="min-h-10 gap-3 px-2.5 py-2 text-[var(--error)]"><LogOut /><span>{logout.isPending ? "正在退出…" : "退出登录"}</span></DropdownMenuItem>
        </> : state?.enabled ? <>
          <DropdownMenuItem asChild className="min-h-10 gap-3 px-2.5 py-2"><Link href="/login"><LogIn /><span>登录</span></Link></DropdownMenuItem>
          <DropdownMenuItem asChild className="min-h-10 gap-3 px-2.5 py-2"><Link href="/register"><UserPlus /><span>注册账号</span></Link></DropdownMenuItem>
          <DropdownMenuSeparator />
          <p className="px-2.5 py-2 text-xs leading-5 text-[var(--ink-subtle)]">登录后会自动认领当前浏览器中的匿名会话。</p>
        </> : <>
          <DropdownMenuItem disabled className="min-h-10 gap-3 px-2.5 py-2 data-disabled:opacity-75"><UserRound /><span>个人信息</span><span className="ml-auto text-xs text-[var(--ink-subtle)]">尚未启用</span></DropdownMenuItem>
          <DropdownMenuItem disabled className="min-h-10 gap-3 px-2.5 py-2 data-disabled:opacity-75"><Settings /><span>设置</span><span className="ml-auto text-xs text-[var(--ink-subtle)]">尚未启用</span></DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem disabled className="min-h-10 gap-3 px-2.5 py-2 data-disabled:opacity-75"><LogOut /><span>退出登录</span><span className="ml-auto text-xs text-[var(--ink-subtle)]">当前未登录</span></DropdownMenuItem>
        </>}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
