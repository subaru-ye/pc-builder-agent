"use client";

import { ChevronUp, LogOut, Settings, UserRound } from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "./ui/dropdown-menu";

export function LocalProfileMenu() {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button type="button" aria-label="打开本地访客菜单" className="group flex min-h-14 w-full items-center gap-3 rounded-lg px-2 text-left outline-none transition-colors hover:bg-[var(--surface-2)] focus-visible:ring-2 focus-visible:ring-[var(--primary)]">
          <span aria-hidden="true" className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg border bg-[var(--surface-2)] text-[var(--ink-muted)]"><UserRound size={17} /></span>
          <span className="min-w-0 flex-1">
            <span className="block truncate text-sm font-medium">本地访客</span>
            <span className="block truncate text-xs text-[var(--ink-subtle)]">匿名会话</span>
          </span>
          <ChevronUp aria-hidden="true" size={16} className="shrink-0 text-[var(--ink-subtle)] transition-transform group-data-[state=open]:rotate-180" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent side="top" align="start" sideOffset={8} className="w-64 border border-[var(--hairline)] bg-[var(--surface-3)] p-1.5">
        <DropdownMenuLabel className="px-2 py-2">
          <span className="block text-sm font-medium text-[var(--ink)]">本地匿名模式</span>
          <span className="mt-0.5 block font-normal text-[var(--ink-subtle)]">会话只属于当前浏览器身份</span>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem disabled className="min-h-10 gap-3 px-2.5 py-2 data-disabled:opacity-75">
          <UserRound />
          <span>个人信息</span>
          <span className="ml-auto text-xs text-[var(--ink-subtle)]">尚未开放</span>
        </DropdownMenuItem>
        <DropdownMenuItem disabled className="min-h-10 gap-3 px-2.5 py-2 data-disabled:opacity-75">
          <Settings />
          <span>设置</span>
          <span className="ml-auto text-xs text-[var(--ink-subtle)]">尚未开放</span>
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem disabled className="min-h-10 gap-3 px-2.5 py-2 data-disabled:opacity-75">
          <LogOut />
          <span>退出登录</span>
          <span className="ml-auto text-xs text-[var(--ink-subtle)]">当前未登录</span>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
