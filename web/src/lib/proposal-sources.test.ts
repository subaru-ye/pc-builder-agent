import { describe, expect, it } from "vitest";
import recording from "../../../internal/planning/testdata/complete_proposal_recording.json";
import { groupProposalSources, retrievalSummary, type Source } from "./proposal-sources";
import type { Session } from "./api/types";

type Result = NonNullable<Session["proposal"]>["result"];
const result = recording.result as unknown as Result;

describe("proposal source presentation", () => {
  it("does not describe 100 saved catalog sources as live browsing", () => {
    expect(result.evidence).toHaveLength(100);
    expect(retrievalSummary(result)).toBe("本轮未联网，使用本地资料");
    const groups = groupProposalSources(result);
    expect(groups.reduce((sum, g) => sum + g.entries.length, 0)).toBe(100);
    expect(groups.some((g) => g.used)).toBe(true);
    expect(groups.some((g) => !g.used)).toBe(true);
  });
  it("groups by URL while retaining distinct fields and timestamps", () => {
    const source: Source = { id: "one", candidate_id: result.candidates[0].id, field: "socket", kind: "catalog", url: "https://example.com/spec", title: "型号资料", text: "AM4", captured_at: "2026-07-28" };
    const groups = groupProposalSources({ ...result, evidence: [source, { ...source, id: "two", field: "tdp_w", captured_at: "2026-07-29" }] });
    expect(groups).toHaveLength(1);
    expect(groups[0].used).toBe(true);
    expect(groups[0].entries.map((e) => e.field)).toEqual(["socket", "tdp_w"]);
    expect(groups[0].entries[1].captured_at).toBe("2026-07-29");
  });
  it("distinguishes restored external sources and missing historical usage", () => {
    expect(retrievalSummary({ ...result, evidence: [{ ...result.evidence[0], kind: "page" }] })).toContain("复用已保存资料");
    expect(retrievalSummary({ ...result, search_calls: undefined })).toContain("未记录");
  });
  it("keeps unused excerpts secondary even when they share a vendor URL", () => {
    const source: Source = { id: "chosen", candidate_id: result.candidates[0].id, kind: "catalog", url: "https://example.com/series", title: "系列规格", text: "已选配件规格", captured_at: "2026-07-28" };
    const groups = groupProposalSources({ ...result, evidence: [source, { ...source, id: "other", candidate_id: "unselected", text: "其他型号规格" }] });
    expect(groups.find((g) => g.used)?.entries.map((e) => e.id)).toEqual(["chosen"]);
    expect(groups.find((g) => !g.used)?.entries.map((e) => e.id)).toEqual(["other"]);
  });
});
