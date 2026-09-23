import type { RequirementState, Session } from "@/lib/api/types";

/** 完整 Session v2 合同夹具:三轴字段齐全,按用例覆盖。 */
export function makeRequirementState(fields: RequirementState["fields"] = {}, overrides: Partial<RequirementState> = {}): RequirementState {
  return { schema_version: 2, revision: 1, fields, alternatives: [], changes: [], history: [], ...overrides };
}

export function makeSession(overrides: Partial<Session> = {}): Session {
  return {
    schema_version: 1, id: "session-1", title: "帮朋友装机", phase: "collecting", created_at: "2026-09-09T01:00:00Z", updated_at: "2026-09-09T01:00:00Z", version_count: 0,
    messages: [], pending_requirement: null, active_run: null, last_error: null, recovery_phase: null, degraded: false,
    requirement_state: makeRequirementState(),
    requirement_readiness: { status: "incomplete", missing_fields: ["budget_cny", "use_case.type"], blocking_conflicts: [], unsupported_capabilities: [], next_question: null, confirmation_eligible: false, effective_defaults: [] },
    review_spec: null, review_hash: null, effective_budget_ceiling_cny: null,
    requirement_confirmation: { status: "unconfirmed", confirmed_revision: null, confirmed_at: null, confirmed_review_hash: null, review_diff: null },
    build_relation: { status: "none", version: null, snapshot_id: null, review_hash: null, builder_input_hash: null, retry_run_id: null },
    proposal: undefined,
    ...overrides,
  } satisfies Session;
}
