import type { CaseResponse, CompareResponse, ProvenanceResponse, RunsResponse, RunSummary, TimelineResponse } from "./types";

async function read<T>(path: string, params?: Record<string, string>): Promise<T> {
  const response = await fetch(`/api/evaldesk/${path}${params ? `?${new URLSearchParams(params)}` : ""}`, { cache: "no-store" });
  if (!response.ok) throw new Error("无法读取评估产物。请确认本机评估服务已启动，或刷新后重新选择运行。历史记录未改动。");
  try { return await response.json() as T; }
  catch { throw new Error("评估服务返回了无法识别的内容。请检查本机服务与前端代理是否已启动。"); }
}

export const evaldesk = {
  runs: () => read<RunsResponse>("runs"),
  compare: (baseline: string, candidate: string) => read<CompareResponse>("compare", { baseline, candidate }),
  case: (baseline: string, candidate: string, id: string) => read<CaseResponse>("cases", { baseline, candidate, case: id }),
  timeline: () => read<TimelineResponse>("timeline"),
  provenance: (run: string) => read<ProvenanceResponse>("provenance", { run }),
};

export const number = (value: number | null | undefined) => value == null ? "未记录" : value.toLocaleString("zh-CN");
export const percent = (value: number | null | undefined) => value == null ? "未记录" : `${(value * 100).toFixed(1)}%`;
export const delta = (value: number | null | undefined, suffix = "") => value == null ? "未知" : `${value > 0 ? "+" : ""}${number(value)}${suffix}`;
export const short = (value: string | null | undefined) => value ? value.slice(0, 10) : "未记录";

// 时间和成绩只取元数据；目录名留在证据中，不用文件名补猜缺失时间。
export function runTime(value: string | null): string {
  if (!value || Number.isNaN(new Date(value).getTime())) return "时间未记录";
  const parts = new Intl.DateTimeFormat("zh-CN", {
    timeZone: "Asia/Shanghai", year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23",
  }).formatToParts(new Date(value));
  const part = (type: Intl.DateTimeFormatPartTypes) => parts.find(p => p.type === type)?.value ?? "";
  return `${part("year")}-${part("month")}-${part("day")} ${part("hour")}:${part("minute")}:${part("second")}`;
}

export function runOutcome(run: RunSummary): string {
  const m = run.original;
  return run.status === "invalid" ? "成绩待核验"
    : run.status === "incomplete" ? `未完成 · 已记录 ${m.recorded}${m.expected == null ? " 次" : `/${m.expected}`}`
    : m.recorded === 0 ? "成绩未记录"
    : `通过 ${m.passed}/${m.expected ?? m.recorded}${run.status === "legacy" ? " · 旧格式" : ""}`;
}

export function runCaption(run: RunSummary): string {
  return `${runTime(run.createdAt)} · 题库 ${run.versions.suite ?? "未记录"} · ${runOutcome(run)}`;
}
