import { describe, expect, it } from "vitest";
import type { ReqV2RunSummary } from "./reqv2";
import { groupRunExperiments } from "./run-experiments";

const run = (dir_name: string, created_at = "2026-09-24T00:00:00Z") => ({ id: dir_name, dir_name, created_at }) as ReqV2RunSummary;

describe("测试目的分组", () => {
  it("复用同一个 Flash 基线，修复前 Qwen 诊断不混入当前模型实验", () => {
    const flash = run("screening-fix2-live-devcal-r3-20260924");
    const groups = groupRunExperiments([flash, run("modelcmp-glm-devcal-r3-20260924"), run("spec6-cert-live-d-swap-qwen-max-devcal-r3-20260924")]);
    const models = groups.find(group => group.purpose === "models")!;
    expect(models.runs).toHaveLength(2);
    expect(models.runs.find(entry => entry.baseline)?.run).toBe(flash);
    expect(groups.find(group => group.purpose === "repair")?.runs[0].run).toBe(flash);
    expect(groups.find(group => group.purpose === "diagnostic")?.runs).toHaveLength(1);
  });

  it("只认准确历史目录名，未知记录保留且不自动归因", () => {
    const unknown = run("modelcmp-unreviewed-devcal-r3-20261008");
    const groups = groupRunExperiments([unknown]);
    expect(groups).toHaveLength(1);
    expect(groups[0].purpose).toBe("unknown");
    expect(groups[0].runs[0].run).toBe(unknown);
    expect(groupRunExperiments([])).toEqual([]);
  });

  it("按各实验最新运行排序，不依赖传入的目录顺序", () => {
    const groups = groupRunExperiments([run("screening-fix-live-devcal-r3-20260924"), run("modelcmp-glm-devcal-r3-20260924", "2026-09-25T00:00:00Z")]);
    expect(groups.map(group => group.purpose)).toEqual(["models", "repair"]);
  });
});
