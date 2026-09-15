import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { BuildView, RequirementSpec } from "@/lib/api/types";
import { RequirementReadOnly } from "./build-inspector";

describe("historical requirement", () => {
  it("renders v1 snapshots with omitted optional titles without changing them", () => {
    // The API permits omission; generated TS applies the schema's defaults.
    const requirement: RequirementSpec = JSON.parse('{"schema_version":1,"budget_cny":7000,"use_case":{"type":"productivity"}}');
    const original = JSON.stringify(requirement);
    render(<RequirementReadOnly build={{ requirement, disclaimers: [] } as unknown as BuildView} />);
    expect(screen.getByText("¥7000")).toBeInTheDocument();
    expect(screen.queryByText("目标应用 / 游戏")).not.toBeInTheDocument();
    expect(screen.getAllByText("未记录")).toHaveLength(3);
    expect(screen.queryByText("NaN%")).not.toBeInTheDocument();
    expect(JSON.stringify(requirement)).toBe(original);
  });
});
