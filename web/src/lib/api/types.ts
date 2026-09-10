import { z } from "zod";
import type { components } from "./generated";
import { schemas } from "./generated-schemas";

export type Session = components["schemas"]["Session"];
export type SessionSummary = components["schemas"]["SessionSummary"];
export type Run = components["schemas"]["Run"];
export type Feedback = components["schemas"]["Feedback"];
export type FeedbackReason = components["schemas"]["FeedbackReason"];
export type RequirementSpec = components["schemas"]["RequirementSpec"];
export type RequirementState = components["schemas"]["RequirementState"];
export type RequirementOperation = components["schemas"]["RequirementOperation"];
export type BuildSummary = components["schemas"]["BuildSummary"];
export type BuildView = components["schemas"]["BuildView"];
export type BuildDiff = components["schemas"]["BuildDiff"];
export type Problem = components["schemas"]["Problem"];
export type Readiness = components["schemas"]["Readiness"];
export type PartCategory = components["schemas"]["PartCategory"];
export type Share = components["schemas"]["Share"];
export type ShareRecord = components["schemas"]["ShareRecord"];
export type PublicBuildView = components["schemas"]["PublicBuildView"];
export type AuthState = components["schemas"]["AuthState"];
export type Account = components["schemas"]["Account"];

const useCaseSchema = z.discriminatedUnion("type", [
  z.object({
    type: z.literal("gaming"),
    titles: z.array(z.string()).default([]),
    resolution: z.enum(["1080p", "2K", "4K"]),
    fps_target: z.number().int().positive().optional(),
  }),
  z.object({
    type: z.literal("productivity"),
    titles: z.array(z.string()).default([]),
    resolution: z.enum(["1080p", "2K", "4K"]).optional(),
    fps_target: z.number().int().positive().optional(),
  }),
  z.object({
    type: z.literal("general"),
    titles: z.array(z.string()).default([]),
    resolution: z.enum(["1080p", "2K", "4K"]).optional(),
    fps_target: z.number().int().positive().optional(),
  }),
]);

// openapi-zod-client 对嵌套 if/then 降级为 unknown；其余字段仍直接来自生成结果。
export const requirementSchema = schemas.RequirementSpec.extend({ use_case: useCaseSchema });

export type RequirementFormValue = z.input<typeof requirementSchema>;

export type RunEventName =
  | "run.started"
  | "run.progress"
  | "requirement.ready"
  | "requirement.updated"
  | "assistant.delta"
  | "assistant.completed"
  | "build.saved"
  | "run.failed"
  | "run.completed";

export interface RunEvent {
  id: string;
  event: RunEventName;
  data: {
    schema_version: 1;
    run_id: string;
    timestamp: string;
    payload: Record<string, unknown>;
  };
}
