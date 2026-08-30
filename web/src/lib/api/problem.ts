import { ApiError } from "./client";

const copy: Record<string, string> = {
  not_found: "会话不存在或已失效。",
  session_busy: "当前会话仍在处理，请等待本次运行结束。",
  invalid_session_phase: "当前阶段不能执行这项操作，请刷新会话状态。",
  context_expired: "改单上下文已过期，请新建会话并整单生成。",
  upstream_unavailable: "生成服务暂时不可用，已保存的数据不会丢失。",
  run_timeout: "本次运行超过 10 分钟，已停止并可重试。",
  run_interrupted: "服务重启中断了本次运行，可以显式重试。",
  events_expired: "运行事件已过期，正在改用状态查询恢复。",
  schema_validation_failed: "需求字段没有通过校验，请检查标记的内容。",
  auth_disabled: "当前环境尚未启用账号功能，仍可继续匿名使用。",
  auth_invalid_credentials: "邮箱或密码不正确。",
  auth_email_exists: "该邮箱已注册，请直接登录。",
  auth_weak_password: "密码至少 10 位，且不能超过 128 位。",
  auth_session_expired: "登录已失效，请重新登录。",
  auth_unavailable: "账号服务暂不可用。登录数据没有降级为访客，请稍后重试。",
};

export function userMessage(error: unknown): string {
  if (error instanceof ApiError) return copy[error.problem.code] ?? error.problem.detail ?? error.problem.title;
  if (error instanceof Error) return error.message;
  return "发生未知错误，请稍后重试。";
}
