import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { BuildView } from "@/lib/api/types";
import { RequirementReadOnly } from "./build-inspector";

describe("historical requirement", () => {
  const spec = {
    schema_version: 2, budget_cny: 7000, budget_flex: 0.1, configuration_scope: ["tower"],
    use_case: { type: "gaming", titles: ["CS2"], performance_goal: "balanced" },
    size_pref: "any", noise_pref: "any", existing_parts: [], priority: [], notes: "",
  };
  const build = (requirement: unknown) => ({ requirement, disclaimers: [] } as unknown as BuildView);

  it("renders the canonical review spec stored with the build", () => {
    render(<RequirementReadOnly build={build(spec)} />);
    expect(screen.getByText(/¥7,000/)).toBeInTheDocument();
    expect(screen.getByText("CS2")).toBeInTheDocument();
  });

  it("prefers the frozen effective constraints of archived planning inputs", () => {
    const planningInput = {
      schema_version: 2,
      requirement_state: { schema_version: 2, revision: 9, fields: {}, alternatives: [], changes: [], history: [] },
      effective_constraints: { spec, defaults: [] },
    };
    render(<RequirementReadOnly build={build(planningInput)} />);
    expect(screen.getByText(/¥7,000/)).toBeInTheDocument();
    expect(screen.queryByText("¥0")).not.toBeInTheDocument();
  });
});
