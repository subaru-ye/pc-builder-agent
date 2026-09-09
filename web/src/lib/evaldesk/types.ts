export type Nullable<T> = T | null;
export type RunStatus = 'complete' | 'incomplete' | 'legacy' | 'invalid';
export interface Usage {
  modelCalls: number; embeddingCalls: number; usageResponses: number;
  inputTokens: number; outputTokens: number; totalTokens: number;
  measuredRecords: number; totalRecords: number; complete: boolean;
}
export interface Metrics {
  passed: number; recorded: number; expected: Nullable<number>; failed: number; dataErrors: number;
  executionRate: Nullable<number>; allPassedCases: number; caseCount: number;
  allPassedRate: Nullable<number>; repeats: Nullable<number>; usage: Nullable<Usage>; durationMs: number;
}
export interface Versions {
  suite: Nullable<string>; suiteHash: Nullable<string>; snapshotDate: Nullable<string>;
  dataFingerprint: Nullable<string>; dataFingerprintSource: Nullable<string>;
  promptVersion: Nullable<string>; promptSnapshot: boolean;
  commit: Nullable<string>; dirty: Nullable<boolean>; binary: Nullable<string>;
  sourceFingerprint: Nullable<string>; grader: Nullable<string>;
  models: { role: string; model: string; provider: Nullable<string> }[];
}
export interface RunSummary {
  id: string; label: string; createdAt: Nullable<string>; status: RunStatus;
  verified: boolean; notes: string[]; versions: Versions;
  original: Metrics; current: Nullable<Metrics>; regradedTrials: Nullable<number>;
}
export interface RunsResponse { runs: RunSummary[]; warnings: string[]; currentGrader: string }
export type CaseStatus = 'regressed' | 'improved' | 'persistent_failure' | 'output_changed' | 'unchanged' | 'added' | 'removed' | 'modified' | 'unavailable';
export interface CaseScore { passed: number; total: number; expected: Nullable<number> }
export interface CaseComparison {
  id: string; title: string; stage: string; status: CaseStatus; content: 'common' | 'added' | 'removed' | 'modified' | 'unknown';
  originalA: Nullable<CaseScore>; originalB: Nullable<CaseScore>;
  currentA: Nullable<CaseScore>; currentB: Nullable<CaseScore>;
  outputChanges: number; usageA: Nullable<Usage>; usageB: Nullable<Usage>;
  callsDelta: Nullable<number>; tokensDelta: Nullable<number>; durationDeltaMs: Nullable<number>;
}
export interface Condition { key: string; label: string; baseline: Nullable<string>; candidate: Nullable<string>; state: 'same' | 'changed' | 'unknown' }
export interface ChangeDetail { label: string; baseline: Nullable<string>; candidate: Nullable<string> }
export interface ChangeSummary {
  key: string; label: string; state: 'same' | 'changed' | 'unknown'; summary: string;
  baseline: Nullable<string>; candidate: Nullable<string>; details: ChangeDetail[]; notes: string[];
}
export interface MetricsDelta {
  executionRate: Nullable<number>; allPassedRate: Nullable<number>;
  modelCalls: Nullable<number>; embeddingCalls: Nullable<number>; totalTokens: Nullable<number>; durationMs: Nullable<number>;
}
export interface CompareResponse {
  baseline: RunSummary; candidate: RunSummary; mode: 'strict' | 'observational' | 'unavailable';
  strictReason: Nullable<string>; currentGrader: string; conditions: Condition[]; notices: string[];
  changeSummaries: ChangeSummary[];
  counts: Record<CaseStatus, number> & { common: number };
  metrics: { scope: string; baseline: Nullable<Metrics>; candidate: Nullable<Metrics>; delta: MetricsDelta };
  cases: CaseComparison[];
}
export interface Failure { id: string; name: string; detail: string; veto: boolean }
export interface Verdict { passed: boolean; dataError: boolean; failures: Failure[] }
export interface Trial {
  seed: number; original: Verdict; current: Nullable<Verdict>; attempts: Nullable<number>;
  usage: Nullable<Usage>; durationMs: number; error: Nullable<string>;
  turns: { turn: number; output: string; modelOutputs: string[]; failures: Failure[] }[];
  selection: Nullable<string>;
}
export interface CaseSide {
  runId: string; caseId: string; title: string; stage: string; inputFrozen: boolean;
  inputs: { turn: number; input: string; expected: string }[];
  trials: Trial[]; notes: string[];
}
export interface CaseResponse { caseId: string; baseline: Nullable<CaseSide>; candidate: Nullable<CaseSide> }
export interface CommitSummary {
  hash: Nullable<string>; subject: Nullable<string>; committedAt: Nullable<string>;
  status: 'available' | 'unrecorded' | 'unavailable';
}
export interface CommitFile { path: string; status: 'added' | 'modified' | 'deleted' | 'type_changed' | 'other'; label: string }
export interface CommitDetails extends CommitSummary {
  files: CommitFile[]; filesAvailable: boolean; filesTruncated: boolean; notes: string[];
}
export interface TimelineEntry { run: RunSummary; frozenCaseCount: Nullable<number>; commit: CommitSummary }
export interface TimelineResponse { items: TimelineEntry[]; warnings: string[]; notes: string[] }
export interface FrozenCaseInput {
  turn: number; input: string; inputKind: 'text' | 'structured'; expected: string; expectationSummary: string[];
}
export interface FrozenCase {
  id: string; title: string; stage: string; contentHash: Nullable<string>;
  recordedTrials: number; plannedTrials: Nullable<number>; inputs: FrozenCaseInput[]; notes: string[];
}
export interface ProvenanceResponse {
  run: RunSummary; frozenCaseCount: Nullable<number>; cases: FrozenCase[]; commit: CommitDetails; notes: string[];
}
