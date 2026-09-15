import type { components } from "./generated";
import { z } from "zod";

const Health = z.object({
  schema_version: z.number().int(),
  status: z.string(),
});
const Readiness = z.object({
  schema_version: z.number().int(),
  status: z.enum(["ready", "degraded", "unavailable"]),
  dependencies: z.object({
    postgres: z.enum(["ok", "unavailable"]),
    redis: z.enum(["ok", "degraded", "unavailable"]),
    buildsvc: z.enum(["ok", "unavailable"]),
    auth: z.enum(["ok", "unavailable"]).optional(),
  }),
});
const Account = z.object({
  id: z.string().uuid(),
  email: z.string().email(),
  display_name: z.string().min(1).max(40),
  created_at: z.string().datetime({ offset: true }),
});
const AuthState = z.object({
  schema_version: z.number().int(),
  enabled: z.boolean(),
  authenticated: z.boolean(),
  account: z.union([Account, z.null()]),
  claimed_session_count: z.number().int().gte(0),
});
const RegisterRequest = z.object({
  schema_version: z.number().int(),
  email: z.string().max(254).email(),
  password: z.string().min(10).max(128),
  display_name: z.string().min(1).max(40),
});
const LoginRequest = z.object({
  schema_version: z.number().int(),
  email: z.string().max(254).email(),
  password: z.string().min(1).max(128),
});
const ProfileRequest = z.object({
  schema_version: z.number().int(),
  display_name: z.string().min(1).max(40),
});
const PasswordChangeRequest = z.object({
  schema_version: z.number().int(),
  current_password: z.string().min(1).max(128),
  new_password: z.string().min(10).max(128),
  confirm_password: z.string().min(10).max(128),
});
const SessionPhase = z.enum([
  "collecting",
  "requirement_ready",
  "building",
  "ready",
  "changing",
  "error",
]);
const SessionSummary = z.object({
  status_label: z.string().optional(),
  schema_version: z.number().int(),
  id: z.string(),
  title: z.string(),
  archived: z.boolean().optional(),
  phase: SessionPhase,
  created_at: z.string().datetime({ offset: true }),
  updated_at: z.string().datetime({ offset: true }),
  version_count: z.number().int().gte(0),
});
const Message = z.object({
  schema_version: z.number().int(),
  id: z.string().uuid(),
  role: z.enum(["user", "assistant"]),
  content: z.string(),
  display_content: z.string().optional(),
  run_id: z.union([z.string(), z.null()]).optional(),
  created_at: z.string().datetime({ offset: true }),
});
const RequirementSource = z.object({
  kind: z.enum(["chat", "edit", "confirmed"]),
  message_id: z.string(),
  quote: z.string(),
});
const RequirementField: z.ZodType<components["schemas"]["RequirementField"]> = z.lazy(() =>
  z.object({
    value: z.unknown().optional(),
    status: z.enum(["unknown", "active", "removed", "conflict"]),
    kind: z.enum(["fact", "context", "constraint"]).optional(),
    evidence: z.enum(["stated", "uncertain"]).optional(),
    strength: z.enum(["must", "prefer"]).optional(),
    scope: z.enum(["session", "temporary"]).optional(),
    source: RequirementSource.optional(),
    previous: RequirementField.optional(),
  })
);
const RequirementAlternative = z.object({
  field: z.string(),
  value: z.unknown(),
  strength: z.enum(["must", "prefer"]),
  scope: z.enum(["session", "temporary"]),
  source: RequirementSource,
  kind: z.enum(["fact", "context", "constraint"]).optional(),
});
const RequirementChange = z.object({
  revision: z.number().int().gte(1),
  op: z.enum(["set", "remove", "restore", "alternative", "conflict"]),
  field: z.string(),
  before: RequirementField.optional(),
  after: RequirementField.optional(),
  source: RequirementSource,
});
const RequirementObservation = z.object({
  field: z.string().optional(),
  text: z.string().min(1),
  reason: z.string(),
  source: RequirementSource,
  resolved: z.boolean().optional(),
});
const RequirementState = z.object({
  schema_version: z.number().int(),
  reply: z.string().optional(),
  next_action: z.enum(["collect", "confirm", "plan"]).optional(),
  revision: z.number().int().gte(0),
  fields: z.record(z.string(), RequirementField),
  alternatives: z.array(RequirementAlternative),
  changes: z.array(RequirementChange),
  history: z.array(RequirementChange),
  observations: z.array(RequirementObservation).optional(),
});
const PlanningInput = z
  .object({
    schema_version: z.number().int(),
    requirement_state: RequirementState,
    base_draft: z.object({}).partial().passthrough().optional(),
    previous_proposal: z.object({}).partial().passthrough().optional(),
    request: RequirementSource.optional(),
  })
  .passthrough();
const PartCategory = z.enum([
  "cpu",
  "gpu",
  "motherboard",
  "memory",
  "ssd",
  "psu",
  "case",
  "cooler",
]);
const PlanningCandidate = z
  .object({
    id: z.string(),
    category: PartCategory,
    brand: z.string(),
    model: z.string(),
    specs: z.object({}).partial().passthrough(),
    price_cny: z.union([z.string(), z.null()]),
    evidence: z.array(z.string()),
    field_evidence: z.record(z.string(), z.string()).optional(),
    field_quotes: z.record(z.string(), z.string()).optional(),
    attributes: z.object({}).partial().passthrough().optional(),
    merchant: z.string().optional(),
    currency: z.string().optional(),
    price_observed_at: z.string().optional(),
    unknown: z.array(z.string()).optional(),
    external: z.boolean(),
  })
  .passthrough();
const PlanningEvidence = z
  .object({
    id: z.string(),
    url: z.string(),
    title: z.string(),
    text: z.string(),
    captured_at: z.string(),
    kind: z.string(),
    candidate_id: z.string().optional(),
    field: z.string().optional(),
    final_url: z.string().optional(),
    reader: z.enum(["http", "browser"]).optional(),
    http_status: z.number().int().optional(),
    read_bytes: z.number().int().optional(),
  })
  .passthrough();
const ValidationCheck = z.object({
  rule_id: z.string(),
  outcome: z.enum(["pass", "fail", "unknown"]),
  severity: z.enum(["none", "warning", "error"]),
  observed: z.object({}).partial().passthrough(),
  missing_fields: z.array(z.string()),
  detail: z.string(),
});
const ValidationReport = z.object({
  overall_status: z.enum(["pass", "review", "fail"]),
  checks: z.array(ValidationCheck).min(12).max(12),
});
const PlanningResult = z
  .object({
    schema_version: z.number().int(),
    outcome: z.enum([
      "collect",
      "clarify",
      "proposal",
      "ready",
      "technical_fault",
    ]),
    model_outcome: z.string().optional(),
    build_version: z.number().int().gte(1).optional(),
    delivery: z
      .object({
        status: z.enum([
          "not_applicable",
          "unresolved",
          "eligible",
          "delivered",
          "stale",
        ]),
        issues: z.array(z.string()),
      })
      .passthrough()
      .optional(),
    reply: z.string(),
    draft: z.object({}).partial().passthrough().optional(),
    assessments: z
      .array(
        z
          .object({
            field: z.string(),
            status: z.enum(["met", "unmet", "unknown"]),
            explanation: z.string(),
            evidence: z.array(z.string()),
          })
          .passthrough()
      )
      .optional(),
    issues: z.array(z.string()),
    assumptions: z.array(z.string()),
    candidates: z.array(PlanningCandidate),
    evidence: z.array(PlanningEvidence),
    validation: ValidationReport.optional(),
    quote: z
      .object({
        total_cny: z.string(),
        missing_count: z.number().int(),
        snapshot_date: z.string(),
        snapshot_id: z.number().int().optional(),
      })
      .passthrough()
      .optional(),
    model_calls: z.number().int().optional(),
    tool_calls: z.number().int().optional(),
    search_calls: z.number().int().optional(),
    search_requests: z.number().int().optional(),
    page_calls: z.number().int().optional(),
    read_attempts: z
      .array(
        z
          .object({
            method: z.enum(["http", "browser"]),
            duration_ms: z.number().int(),
            bytes: z.number().int(),
            status: z.number().int(),
            outcome: z.string(),
          })
          .passthrough()
      )
      .optional(),
    tokens: z.number().int().optional(),
    duration_ms: z.number().int().optional(),
    stage_ms: z.record(z.string(), z.number().int()).optional(),
  })
  .passthrough();
const SessionProposal = z
  .object({
    id: z.number().int(),
    requirement: PlanningInput,
    parent_version: z.number().int(),
    created_at: z.string(),
    result: PlanningResult,
  })
  .passthrough();
const RequirementSpec = z.object({
  schema_version: z.number().int(),
  budget_cny: z.number().int().gte(1),
  budget_flex: z.number().gte(0).lte(0.3).optional().default(0.1),
  use_case: z.unknown(),
  size_pref: z.enum(["atx", "matx", "itx", "any"]).optional().default("any"),
  noise_pref: z.enum(["silent", "normal", "any"]).optional().default("any"),
  brand_pref: z
    .object({
      cpu: z.enum(["any", "intel", "amd"]).default("any"),
      gpu: z.enum(["any", "nvidia", "amd"]).default("any"),
    })
    .partial()
    .optional(),
  existing_parts: z.array(PartCategory).optional().default([]),
  budget_basis: z.enum(["new_purchase", "full_build"]).optional(),
  owned_parts: z
    .array(
      z.object({
        category: PartCategory,
        model: z.string().min(1),
        quantity: z.number().int().gte(1).lte(8).optional().default(1),
      })
    )
    .optional(),
  priority: z.array(PartCategory).optional().default([]),
  notes: z.string().optional().default(""),
  constraint_strengths: z.record(z.string(), z.enum(["must", "prefer"])).optional(),
  requirement_semantics: z
    .record(z.string(), z.enum(["fact", "context", "constraint"]))
    .optional(),
  requirement_observations: z.array(RequirementObservation).optional(),
  requirement_details: z
    .object({ appearance: z.string(), recipient: z.string() })
    .partial()
    .optional(),
});
const Problem = z
  .object({
    type: z.string(),
    title: z.string(),
    status: z.number().int().gte(400).lte(599),
    detail: z.string().optional(),
    instance: z.string().optional(),
    code: z.enum([
      "invalid_request",
      "not_found",
      "session_busy",
      "invalid_session_phase",
      "schema_validation_failed",
      "upstream_unavailable",
      "context_expired",
      "run_timeout",
      "run_interrupted",
      "generation_failed",
      "events_expired",
      "internal_error",
      "auth_disabled",
      "auth_invalid_credentials",
      "auth_email_exists",
      "auth_weak_password",
      "auth_session_expired",
      "auth_unavailable",
    ]),
    request_id: z.string(),
  })
  .passthrough();
const Run = z.object({
  schema_version: z.number().int(),
  id: z.string().uuid(),
  session_id: z.string(),
  kind: z.enum(["screening", "build", "change"]),
  status: z.enum(["running", "succeeded", "failed", "interrupted"]),
  started_at: z.string().datetime({ offset: true }),
  finished_at: z.union([z.string(), z.null()]).optional(),
  error: z.union([Problem, z.null()]).optional(),
  events_url: z.string(),
});
const Session = SessionSummary.and(
  z
    .object({
      messages: z.array(Message),
      proposal: SessionProposal.optional(),
      pending_requirement: z.union([RequirementSpec, z.null()]),
      requirement_state: z.union([RequirementState, z.null()]),
      requirement_status: z.enum([
        "collecting",
        "ready_to_confirm",
        "confirmed",
        "modified",
      ]),
      confirmed_requirement_state: z.union([RequirementState, z.null()]),
      confirmed_requirement: z.union([RequirementSpec, z.null()]),
      confirmed_at: z.union([z.string(), z.null()]),
      missing_fields: z.array(z.string()),
      active_run: z.union([Run, z.null()]),
      last_error: z.union([Problem, z.null()]),
      recovery_phase: z.union([
        z.enum(["collecting", "requirement_ready", "ready"]),
        z.null(),
      ]),
      degraded: z.boolean(),
    })
    .passthrough()
);
const updateSession_Body = z
  .object({ title: z.string().min(1).max(80), archived: z.boolean() })
  .partial();
const createMessageRun_Body = z.object({
  schema_version: z.number().int(),
  text: z.string().min(1).max(4000),
});
const RequirementOperation = z.object({
  op: z.enum(["set", "remove", "restore", "alternative", "conflict"]),
  field: z.string(),
  value: z.unknown().optional(),
  strength: z.enum(["must", "prefer"]).optional(),
  scope: z.enum(["session", "temporary"]).optional(),
  quote: z.string().optional(),
  kind: z.enum(["fact", "context", "constraint"]).optional(),
  evidence: z.enum(["stated", "uncertain", "inferred"]).optional(),
});
const updateRequirementState_Body = z.object({
  expected_revision: z.number().int().gte(0),
  operations: z.array(RequirementOperation).min(1).max(32),
});
const FeedbackReason = z.enum([
  "unnecessary_question",
  "requirement_mismatch",
  "configuration_issue",
  "price_issue",
  "unclear_explanation",
  "other",
]);
const Feedback = z.object({
  id: z.string().uuid(),
  run_id: z.string().uuid(),
  reason: FeedbackReason,
  comment: z.string().max(2000),
  fingerprint: z.string().regex(/^[0-9a-f]{64}$/),
  created_at: z.string().datetime({ offset: true }),
  updated_at: z.string().datetime({ offset: true }),
});
const FeedbackResponse = z.object({
  schema_version: z.number().int(),
  feedback: z.union([Feedback, z.null()]),
});
const FeedbackInput = z.object({
  schema_version: z.number().int(),
  reason: FeedbackReason,
  comment: z.string().max(2000).optional(),
});
const Money = z.string();
const PriceFreshness = z.enum(["fresh", "aging", "stale", "unknown"]);
const BuildSummary = z.object({
  schema_version: z.number().int(),
  version: z.number().int().gte(1),
  parent_version: z.union([z.number(), z.null()]).optional(),
  intent: z.string(),
  total_cny: Money.regex(/^-?[0-9]+\.[0-9]{2}$/),
  snapshot_date: z.string(),
  price_freshness: PriceFreshness.optional(),
  overall_status: z.enum(["pass", "review", "fail"]),
  created_at: z.string().datetime({ offset: true }),
});
const SavedPlanningAssessment = z
  .object({
    field: z.string(),
    status: z.enum(["met", "unmet", "unknown"]),
    explanation: z.string(),
    evidence: z.array(z.string()),
  })
  .passthrough();
const PartLine = z.object({
  owned: z.boolean().optional(),
  category: PartCategory,
  sku: z.string(),
  name: z.string(),
  quantity: z.number().int().gte(1),
  unit_price_cny: z.union([Money, z.null()]).optional(),
  subtotal_cny: z.union([Money, z.null()]).optional(),
  rationale: z.string().optional(),
  price_observed_date: z.string().optional(),
  price_freshness: PriceFreshness.optional(),
  price_availability_basis: z
    .enum(["confirmed_stock", "search_listing", "unknown"])
    .optional(),
});
const PriceFreshnessSummary = z.object({
  overall: PriceFreshness,
  oldest_observed_date: z.union([z.string(), z.null()]).optional(),
  max_age_days: z.union([z.number(), z.null()]).optional(),
  fresh_count: z.number().int().gte(0),
  aging_count: z.number().int().gte(0),
  stale_count: z.number().int().gte(0),
  unknown_count: z.number().int().gte(0),
});
const Quote = z.object({
  snapshot_id: z.number().int().optional(),
  budget_known: z.boolean().optional(),
  purchase_total_cny: Money.regex(/^-?[0-9]+\.[0-9]{2}$/).optional(),
  budget_basis: z.enum(["new_purchase", "full_build"]).optional(),
  snapshot_date: z.string(),
  total_cny: Money.regex(/^-?[0-9]+\.[0-9]{2}$/),
  budget_cny: Money.regex(/^-?[0-9]+\.[0-9]{2}$/),
  budget_delta_cny: Money,
  missing_count: z.number().int().gte(0),
  missing_skus: z.array(z.string()),
  price_freshness: PriceFreshnessSummary.optional(),
});
const BuildView = z.object({
  candidate_snapshot: z
    .object({
      candidates: z.array(PlanningCandidate),
      evidence: z.array(PlanningEvidence),
      assessments: z.array(SavedPlanningAssessment),
      assumptions: z.array(z.string()),
      reply: z.string(),
    })
    .partial()
    .passthrough()
    .optional(),
  schema_version: z.number().int(),
  summary: BuildSummary,
  requirement: z.union([RequirementSpec, PlanningInput]),
  parts: z.array(PartLine),
  quote: Quote,
  validation: ValidationReport,
  disclaimers: z.array(z.string()).min(3),
});
const DiffLine = z.object({
  category: PartCategory,
  changed: z.boolean(),
  before: z.string(),
  after: z.string(),
  price_delta_cny: z.union([Money, z.null()]).optional(),
});
const BuildDiff = z.object({
  schema_version: z.number().int(),
  from_version: z.number().int().gte(1),
  to_version: z.number().int().gte(1),
  lines: z.array(DiffLine).min(8).max(8),
  total_delta_cny: Money,
  budget_delta_cny: Money.optional(),
  snapshot_warning: z.string().optional(),
});
const Share = z.object({
  schema_version: z.number().int(),
  id: z.string().uuid(),
  version: z.number().int().gte(1),
  token: z.string(),
  url: z.string().url(),
  created_at: z.string().datetime({ offset: true }),
  revoked_at: z.union([z.string(), z.null()]),
});
const ShareRecord = z.object({
  schema_version: z.number().int(),
  id: z.string().uuid(),
  version: z.number().int().gte(1),
  created_at: z.string().datetime({ offset: true }),
  revoked_at: z.union([z.string(), z.null()]),
});
const PublicBuildSummary = z.object({
  schema_version: z.number().int(),
  version: z.number().int().gte(1),
  parent_version: z.union([z.number(), z.null()]).optional(),
  intent_label: z.string(),
  total_cny: Money.regex(/^-?[0-9]+\.[0-9]{2}$/),
  snapshot_date: z.string(),
  price_freshness: PriceFreshness.optional(),
  overall_status: z.enum(["pass", "review", "fail"]),
  created_at: z.string().datetime({ offset: true }),
});
const PublicRequirementSummary = z.object({
  known_fields: z.array(z.string()).optional(),
  budget_cny: Money.regex(/^-?[0-9]+\.[0-9]{2}$/),
  budget_flex_percent: z.number().int().gte(0),
  use_case: z.object({
    type: z.enum(["gaming", "productivity", "general", "unknown"]),
    titles: z.array(z.string()),
    resolution: z.union([z.enum(["1080p", "2K", "4K"]), z.null()]),
    fps_target: z.union([z.number(), z.null()]),
  }),
  size_pref: z.enum(["atx", "matx", "itx", "any", "unknown"]),
  noise_pref: z.enum(["silent", "normal", "any", "unknown"]),
  brand_pref: z.object({
    cpu: z.enum(["any", "intel", "amd", "unknown"]),
    gpu: z.enum(["any", "nvidia", "amd", "unknown"]),
  }),
  existing_parts: z.array(PartCategory),
  priority: z.array(PartCategory),
});
const PublicValidationCheck = z.object({
  rule_id: z.string(),
  outcome: z.enum(["pass", "fail", "unknown"]),
  severity: z.enum(["none", "warning", "error"]),
  missing_fields: z.array(z.string()),
  detail: z.string(),
});
const PublicBuildView = z.object({
  sources: z
    .array(
      z
        .object({ url: z.string(), title: z.string(), captured_at: z.string() })
        .passthrough()
    )
    .optional(),
  schema_version: z.number().int(),
  summary: PublicBuildSummary,
  requirement: PublicRequirementSummary,
  parts: z.array(PartLine),
  quote: Quote,
  validation: z.object({
    overall_status: z.enum(["pass", "review", "fail"]),
    checks: z.array(PublicValidationCheck).min(12).max(12),
  }),
  disclaimers: z.array(z.string()).min(3),
  share: z.object({ created_at: z.string().datetime({ offset: true }) }),
});

export const schemas = {
  Health,
  Readiness,
  Account,
  AuthState,
  RegisterRequest,
  LoginRequest,
  ProfileRequest,
  PasswordChangeRequest,
  SessionPhase,
  SessionSummary,
  Message,
  RequirementSource,
  RequirementField,
  RequirementAlternative,
  RequirementChange,
  RequirementObservation,
  RequirementState,
  PlanningInput,
  PartCategory,
  PlanningCandidate,
  PlanningEvidence,
  ValidationCheck,
  ValidationReport,
  PlanningResult,
  SessionProposal,
  RequirementSpec,
  Problem,
  Run,
  Session,
  updateSession_Body,
  createMessageRun_Body,
  RequirementOperation,
  updateRequirementState_Body,
  FeedbackReason,
  Feedback,
  FeedbackResponse,
  FeedbackInput,
  Money,
  PriceFreshness,
  BuildSummary,
  SavedPlanningAssessment,
  PartLine,
  PriceFreshnessSummary,
  Quote,
  BuildView,
  DiffLine,
  BuildDiff,
  Share,
  ShareRecord,
  PublicBuildSummary,
  PublicRequirementSummary,
  PublicValidationCheck,
  PublicBuildView,
};
