"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, LockKeyhole, UserRound } from "lucide-react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { FormEvent, useEffect, useState } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api/client";
import { userMessage } from "@/lib/api/problem";
import { queryKeys } from "@/lib/api/query-keys";
import { broadcastAuth } from "@/lib/auth-events";
import { Button } from "./ui/button";
import { Input } from "./ui/input";

export function AccountSettings() {
  const router = useRouter();
  const queryClient = useQueryClient();
  const auth = useQuery({ queryKey: queryKeys.auth, queryFn: api.authState });
  const [displayName, setDisplayName] = useState<string | null>(null);
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  useEffect(() => {
    if (auth.data && (!auth.data.enabled || !auth.data.authenticated)) router.replace("/login?next=/account");
  }, [auth.data, router]);
  const profile = useMutation({
    mutationFn: () => api.updateProfile(displayName ?? auth.data?.account?.display_name ?? "", crypto.randomUUID()),
    onSuccess: (state) => { queryClient.setQueryData(queryKeys.auth, state); broadcastAuth("profile"); toast.success("个人信息已保存"); },
  });
  const password = useMutation({
    mutationFn: () => api.changePassword(currentPassword, newPassword, confirmPassword, crypto.randomUUID()),
    onSuccess: () => { setCurrentPassword(""); setNewPassword(""); setConfirmPassword(""); toast.success("密码已修改"); },
  });
  const logout = useMutation({
    mutationFn: api.logout,
    onSuccess: () => { queryClient.clear(); broadcastAuth("logout"); router.replace("/"); },
  });
  if (auth.isPending || !auth.data?.authenticated || !auth.data.account) return <main className="flex min-h-screen items-center justify-center text-sm text-[var(--ink-muted)]">正在读取账号信息…</main>;
  const account = auth.data.account;
  return (
    <main className="min-h-screen bg-[var(--canvas)] px-4 py-10 sm:px-8">
      <div className="mx-auto max-w-2xl">
        <Link href="/" className="mb-8 inline-flex min-h-11 items-center gap-2 text-sm text-[var(--ink-muted)] hover:text-[var(--ink)]"><ArrowLeft size={16} />返回工作台</Link>
        <h1 className="text-2xl font-semibold tracking-[-0.4px]">账号设置</h1>
        <p className="mt-2 text-sm text-[var(--ink-muted)]">账号由本地 Supabase Auth 管理，产品数据库不保存密码。</p>
        <section className="mt-8 border-t py-6" aria-labelledby="profile-title">
          <div className="flex items-center gap-3"><UserRound size={18} /><h2 id="profile-title" className="text-lg font-semibold">个人信息</h2></div>
          <form onSubmit={(e: FormEvent) => { e.preventDefault(); profile.mutate(); }} className="mt-5 grid gap-5 sm:grid-cols-2">
            <label className="text-sm font-medium">显示名称<Input className="mt-2 h-11" value={displayName ?? account.display_name} onChange={(e) => setDisplayName(e.target.value)} minLength={1} maxLength={40} required /></label>
            <label className="text-sm font-medium">邮箱<Input className="mt-2 h-11" value={account.email} readOnly aria-readonly="true" /></label>
            <div className="sm:col-span-2 flex items-center justify-between gap-4"><p className="text-xs text-[var(--ink-subtle)]">登录方式：邮箱密码</p><Button type="submit" disabled={profile.isPending}>保存名称</Button></div>
            {profile.isError && <p role="alert" className="status-fail sm:col-span-2">{userMessage(profile.error)}</p>}
          </form>
        </section>
        <section id="security" className="border-t py-6" aria-labelledby="security-title">
          <div className="flex items-center gap-3"><LockKeyhole size={18} /><h2 id="security-title" className="text-lg font-semibold">修改密码</h2></div>
          <form onSubmit={(e: FormEvent) => { e.preventDefault(); password.mutate(); }} className="mt-5 space-y-5">
            <label className="block text-sm font-medium">当前密码<Input className="mt-2 h-11" type="password" autoComplete="current-password" value={currentPassword} onChange={(e) => setCurrentPassword(e.target.value)} required /></label>
            <div className="grid gap-5 sm:grid-cols-2"><label className="text-sm font-medium">新密码<Input className="mt-2 h-11" type="password" autoComplete="new-password" minLength={10} maxLength={128} value={newPassword} onChange={(e) => setNewPassword(e.target.value)} required /></label><label className="text-sm font-medium">确认新密码<Input className="mt-2 h-11" type="password" autoComplete="new-password" minLength={10} maxLength={128} value={confirmPassword} onChange={(e) => setConfirmPassword(e.target.value)} required /></label></div>
            <div className="flex justify-end"><Button type="submit" disabled={password.isPending || newPassword !== confirmPassword}>{password.isPending ? "正在修改…" : "修改密码"}</Button></div>
            {password.isError && <p role="alert" className="status-fail">{userMessage(password.error)}</p>}
          </form>
        </section>
        <section className="border-t py-6"><h2 className="text-lg font-semibold">退出当前设备</h2><p className="mt-2 text-sm text-[var(--ink-muted)]">退出后会生成全新的匿名身份，已认领会话不会继续暴露给访客。</p><Button className="mt-5" variant="destructive" onClick={() => logout.mutate()} disabled={logout.isPending}>退出登录</Button></section>
      </div>
    </main>
  );
}
