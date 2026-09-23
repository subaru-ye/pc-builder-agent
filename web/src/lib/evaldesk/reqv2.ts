// Requirement v2 评估工作台的类型与本机只读客户端。
// 字段与 Go 端 internal/evaldesk/reqv2*.go 投影一一对应。
export interface ReqV2ModelIdentity { role: string; model: string; provider?: string }
export interface ReqV2Evidence { status: "complete" | "incomplete" | "invalid"; notes: string[] }
export interface ReqV2RunSummary {
  id: string; dir_name: string; label: string; created_at: string | null;
  mode: "live" | "deterministic" | "replay" | "regrade" | string; zero_model: boolean; regrade: boolean;
  source_run?: string; superseded: boolean; grader_version: string; splits: string[]; repeats: number;
  code_commit?: string; code_dirty: boolean | null; models: ReqV2ModelIdentity[];
  gate_passed: boolean | null; conclusion: string; evidence: ReqV2Evidence;
  manifest_sha256?: string; gates_sha256?: string; max_model_requests?: number;
}
export interface ReqV2GateVerdictRow { layer: string; metric: string; actual: string; threshold: string; passed: boolean; evaluable: boolean; note?: string }
export interface ReqV2LayerRow { cases: number; passed: number; skipped: number; vetoes: number; failure_classifications: Record<string, number> }
export interface ReqV2CaseIndexEntry { layer: string; id: string; split: string; session: string; repeats: number[]; pass_k: boolean; skipped: boolean; vetoes: number; failures: string[] }
export interface ReqV2IntegrityCheck { check: string; state: "ok" | "mismatch" | "missing" | "invalid" | "incomplete"; detail?: string }
export interface ReqV2RunDetail extends ReqV2RunSummary {
  duration_ms: number; gate_verdicts: ReqV2GateVerdictRow[]; per_layer: Record<string, ReqV2LayerRow>;
  model_quality: Record<string, unknown>; usage: unknown; limitations: string[];
  cases: ReqV2CaseIndexEntry[]; regrade_plan?: unknown; integrity_checks: ReqV2IntegrityCheck[];
  gate_thresholds?: unknown;
}
export interface ReqV2RunsResponse { runs: ReqV2RunSummary[]; warnings: string[] }

export interface ReqV2AssertionRow { name: string; pass: boolean; detail?: string; classification?: string }
export interface ReqV2TurnRow { index: number; reply?: string; operations?: string[]; turn_signals?: string[]; duration_ms?: number; screen_model_called: boolean }
export interface ReqV2RepeatEvidence {
  repeat: number; pass: boolean; skipped?: string; error?: string; vetoes?: string[];
  assertions: ReqV2AssertionRow[]; turns?: ReqV2TurnRow[]; observation_rest?: Record<string, unknown>;
}
export interface ReqV2FrozenCase { layer: string; id: string; title?: string; split?: string; session?: string; rationale?: string; fields: Record<string, unknown>; sha256: string }
export interface ReqV2CaseDetail {
  run: string; layer: string; case: string; split: string; session: string; pass_k: boolean;
  repeats: ReqV2RepeatEvidence[]; frozen?: ReqV2FrozenCase; frozen_note?: string; integrity: ReqV2Evidence;
}
export interface ReqV2CompareCaseSet { baseline_cases: number; candidate_cases: number; common: number; only_in_baseline?: string[]; only_in_candidate?: string[] }
export interface ReqV2CompareOutcome {
  manifest_same: boolean; grader_same: boolean; model_pairs: number; still_pass: number; still_fail: number;
  regressed: string[]; newly_passing: string[]; mcnemar_p?: number; mcnemar_note?: string; sample_note?: string;
  deterministic_layers: Record<string, { baseline_pass: number; candidate_pass: number; total: number }>;
  model_layers: Record<string, { baseline_pass: number; candidate_pass: number; total: number }>;
}
export interface ReqV2CompareResponse {
  strict: boolean; reasons?: string[]; baseline: ReqV2RunSummary; candidate: ReqV2RunSummary;
  outcome?: ReqV2CompareOutcome; case_set: ReqV2CompareCaseSet;
}

async function read<T>(path: string, params?: Record<string, string>): Promise<T> {
  const response = await fetch(`/api/evaldesk/requirement-v2/${path}${params ? `?${new URLSearchParams(params)}` : ""}`, { cache: "no-store" });
  if (!response.ok) throw new Error("无法读取 Requirement v2 产物。请确认本机评估服务已启动,或刷新重新读取;历史记录未改动。");
  try { return await response.json() as T; }
  catch { throw new Error("评估服务返回了无法识别的内容。请检查本机服务与前端代理是否已启动。"); }
}

export const reqv2 = {
  runs: () => read<ReqV2RunsResponse>("runs"),
  run: (id: string) => read<ReqV2RunDetail>("run", { id }),
  case: (id: string, layer: string, caseId: string) => read<ReqV2CaseDetail>("case", { id, layer, case: caseId }),
  compare: (a: string, b: string) => read<ReqV2CompareResponse>("compare", { a, b }),
};

export const reqV2ModeLabels: Record<string, string> = {
  live: "真实调用", deterministic: "零模型确定性", replay: "重放", regrade: "零模型重判",
};

export const reqV2LayerLabels: Record<string, string> = {
  extraction: "初筛抽取", conversations: "多轮对话", reducer: "Reducer", readiness: "Readiness", policy: "Policy", "ui-contract": "UI 合同",
};

export function reqV2Time(value: string | null): string {
  if (!value || Number.isNaN(new Date(value).getTime())) return "时间未记录";
  const parts = new Intl.DateTimeFormat("zh-CN", {
    timeZone: "Asia/Shanghai", year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit", hourCycle: "h23",
  }).formatToParts(new Date(value));
  const part = (type: Intl.DateTimeFormatPartTypes) => parts.find(p => p.type === type)?.value ?? "";
  return `${part("year")}-${part("month")}-${part("day")} ${part("hour")}:${part("minute")}`;
}
