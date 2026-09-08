import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { RequirementForm } from "./requirement-form";
import type { RequirementSpec } from "@/lib/api/types";

const requirement: RequirementSpec = {
  schema_version: 1, budget_cny: 8000, budget_flex: 0.1,
  use_case: { type: "gaming", titles: ["黑神话：悟空"], resolution: "2K", fps_target: 60 },
  size_pref: "any", noise_pref: "normal", brand_pref: { cpu: "any", gpu: "any" },
  existing_parts: [], priority: ["gpu"], notes: "",
};

describe("RequirementForm", () => {
  it("requires an exact owned model and preserves the purchase budget basis", async () => {
    const confirm = vi.fn();
    render(<RequirementForm value={{ ...requirement, existing_parts: ["cpu"] }} busy={false} onSave={vi.fn()} onConfirm={confirm} />);
    await userEvent.click(screen.getByRole("button", { name: "确认并生成配置" }));
    expect(confirm).not.toHaveBeenCalled();
    await userEvent.type(screen.getByLabelText("已有处理器完整型号"), "AMD Ryzen 5 7600");
    await userEvent.click(screen.getByRole("button", { name: "确认并生成配置" }));
    expect(confirm).not.toHaveBeenCalled();
    await userEvent.selectOptions(screen.getByLabelText("预算口径"), "new_purchase");
    await userEvent.click(screen.getByRole("button", { name: "确认并生成配置" }));
    expect(confirm).toHaveBeenCalledWith(expect.objectContaining({
      budget_basis: "new_purchase",
      owned_parts: [{ category: "cpu", model: "AMD Ryzen 5 7600", quantity: 1 }],
    }), true);
  });
  it("hides gaming-only fields without inventing replacement values", async () => {
    render(<RequirementForm value={requirement} busy={false} onSave={vi.fn()} onConfirm={vi.fn()} />);
    await userEvent.selectOptions(screen.getByLabelText("主要用途"), "productivity");
    expect(screen.queryByLabelText("游戏（逗号分隔）")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("目标分辨率")).not.toBeInTheDocument();
  });

  it("saves before confirm when the form is dirty", async () => {
    const actions: string[] = [];
    render(<RequirementForm value={requirement} busy={false} onSave={async () => { actions.push("save"); }} onConfirm={async (_value, dirty) => { actions.push(`confirm:${dirty}`); }} />);
    const budget = screen.getByLabelText("预算（元）");
    await userEvent.clear(budget);
    await userEvent.type(budget, "7500");
    await userEvent.click(screen.getByRole("button", { name: "确认并生成配置" }));
    expect(actions).toEqual(["confirm:true"]);
  });
});
