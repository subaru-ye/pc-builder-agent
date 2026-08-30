"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Eye, EyeOff, LockKeyhole } from "lucide-react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { FormEvent, useState } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api/client";
import { userMessage } from "@/lib/api/problem";
import { queryKeys } from "@/lib/api/query-keys";
import { broadcastAuth } from "@/lib/auth-events";
import { Button } from "./ui/button";
import { Input } from "./ui/input";

export function AuthForm({ mode, nextPath }: { mode: "login" | "register"; nextPath: string }) {
  const router = useRouter();
  const queryClient = useQueryClient();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const action = useMutation({
    mutationFn: () => mode === "login"
      ? api.login(email, password, crypto.randomUUID())
      : api.register(email, password, displayName, crypto.randomUUID()),
    onSuccess: (state) => {
      queryClient.setQueryData(queryKeys.auth, state);
      void queryClient.invalidateQueries({ queryKey: queryKeys.sessions });
      broadcastAuth("login");
      const claimed = state.claimed_session_count;
      toast.success(claimed > 0 ? `已登录，并认领 ${claimed} 个匿名会话` : mode === "login" ? "登录成功" : "注册成功");
      router.replace(nextPath);
      router.refresh();
    },
  });
  function submit(event: FormEvent) {
    event.preventDefault();
    if (action.isPending) return;
    action.mutate();
  }

  const isRegister = mode === "register";
  return (
    <main className="flex min-h-screen items-center justify-center bg-[var(--canvas)] px-4 py-12">
      <section className="w-full max-w-md" aria-labelledby="auth-title">
        <Link href="/" className="mb-8 inline-flex min-h-11 items-center gap-2 text-sm text-[var(--ink-muted)] hover:text-[var(--ink)]"><ArrowLeft size={16} />返回工作台</Link>
        <div className="mb-6 flex h-11 w-11 items-center justify-center rounded-lg border bg-[var(--surface-1)]"><LockKeyhole size={20} /></div>
        <h1 id="auth-title" className="text-2xl font-semibold tracking-[-0.4px]">{isRegister ? "创建本地账号" : "登录本地账号"}</h1>
        <p className="mt-2 text-sm leading-6 text-[var(--ink-muted)]">{isRegister ? "注册后会自动认领当前浏览器里的匿名会话。" : "登录后可在不同浏览器中查看已经认领的会话。"}</p>
        <form onSubmit={submit} className="mt-8 space-y-5 rounded-xl border bg-[var(--surface-1)] p-5 sm:p-6">
          {isRegister && <label className="block text-sm font-medium">显示名称<Input className="mt-2 h-11" value={displayName} onChange={(e) => setDisplayName(e.target.value)} autoComplete="name" minLength={1} maxLength={40} required /></label>}
          <label className="block text-sm font-medium">邮箱<Input className="mt-2 h-11" type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="email" maxLength={254} required /></label>
          <label className="block text-sm font-medium">密码
            <span className="relative mt-2 block"><Input className="h-11 pr-12" type={showPassword ? "text" : "password"} value={password} onChange={(e) => setPassword(e.target.value)} autoComplete={isRegister ? "new-password" : "current-password"} minLength={isRegister ? 10 : 1} maxLength={128} required /><button type="button" onClick={() => setShowPassword((v) => !v)} aria-label={showPassword ? "隐藏密码" : "显示密码"} className="absolute right-0 top-0 flex h-11 w-11 items-center justify-center rounded-lg text-[var(--ink-muted)] hover:text-[var(--ink)] focus-visible:ring-2 focus-visible:ring-[var(--primary)]">{showPassword ? <EyeOff size={17} /> : <Eye size={17} />}</button></span>
          </label>
          {isRegister && <p className="text-xs leading-5 text-[var(--ink-subtle)]">本地环境暂不验证邮箱，忘记密码尚未开放，请妥善保存密码。密码至少 10 位。</p>}
          {action.isError && <p role="alert" tabIndex={-1} className="status-fail text-sm">{userMessage(action.error)}</p>}
          <Button type="submit" size="lg" className="h-11 w-full" disabled={action.isPending}>{action.isPending ? "正在提交…" : isRegister ? "注册并认领会话" : "登录"}</Button>
        </form>
        <p className="mt-5 text-center text-sm text-[var(--ink-muted)]">{isRegister ? "已有账号？" : "还没有账号？"}<Link className="ml-2 text-[var(--primary)] hover:underline" href={isRegister ? `/login?next=${encodeURIComponent(nextPath)}` : `/register?next=${encodeURIComponent(nextPath)}`}>{isRegister ? "直接登录" : "创建账号"}</Link></p>
      </section>
    </main>
  );
}
