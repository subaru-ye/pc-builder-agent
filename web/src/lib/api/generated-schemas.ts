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
  }),
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
  schema_version: z.number().int(),
  id: z.string(),
  title: z.string(),
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
  run_id: z.union([z.string(), z.null()]).optional(),
  created_at: z.string().datetime({ offset: true }),
});
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
  priority: z.array(PartCategory).optional().default([]),
  notes: z.string().optional().default(""),
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
      pending_requirement: z.union([RequirementSpec, z.null()]),
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
const createMessageRun_Body = z.object({
  schema_version: z.number().int(),
  text: z.string().min(1).max(4000),
});
const Money = z.string();
const BuildSummary = z.object({
  schema_version: z.number().int(),
  version: z.number().int().gte(1),
  parent_version: z.union([z.number(), z.null()]).optional(),
  intent: z.string(),
  total_cny: Money.regex(/^-?[0-9]+\.[0-9]{2}$/),
  snapshot_date: z.string(),
  overall_status: z.enum(["pass", "review", "fail"]),
  created_at: z.string().datetime({ offset: true }),
});
const PartLine = z.object({
  category: PartCategory,
  sku: z.string(),
  name: z.string(),
  quantity: z.number().int().gte(1),
  unit_price_cny: z.union([Money, z.null()]).optional(),
  subtotal_cny: z.union([Money, z.null()]).optional(),
  rationale: z.string().optional(),
});
const Quote = z.object({
  snapshot_date: z.string(),
  total_cny: Money.regex(/^-?[0-9]+\.[0-9]{2}$/),
  budget_cny: Money.regex(/^-?[0-9]+\.[0-9]{2}$/),
  budget_delta_cny: Money,
  missing_count: z.number().int().gte(0),
  missing_skus: z.array(z.string()),
});
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
const BuildView = z.object({
  schema_version: z.number().int(),
  summary: BuildSummary,
  requirement: RequirementSpec,
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
  token: z.string(),
  url: z.string().url(),
  created_at: z.string().datetime({ offset: true }),
});
const PublicBuildView = BuildView.and(
  z
    .object({
      share: z
        .object({ created_at: z.string().datetime({ offset: true }) })
        .passthrough(),
    })
    .passthrough()
);

export const schemas = {
  Health,
  Readiness,
  SessionPhase,
  SessionSummary,
  Message,
  PartCategory,
  RequirementSpec,
  Problem,
  Run,
  Session,
  createMessageRun_Body,
  Money,
  BuildSummary,
  PartLine,
  Quote,
  ValidationCheck,
  ValidationReport,
  BuildView,
  DiffLine,
  BuildDiff,
  Share,
  PublicBuildView,
};
