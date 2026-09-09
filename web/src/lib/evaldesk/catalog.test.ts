import { describe, expect, it } from "vitest";
import { getSuiteVersions, groupCommitRuns, groupSuiteRuns, latestRuns, UNRECORDED_SUITE_VERSION } from "./catalog";
import type { RunSummary, TimelineEntry } from "./types";

function run(id: string, suite: string | null, hash: string | null, createdAt: string | null = "2026-09-09T13:44:47.4133304+08:00"): RunSummary {
  return {
    id, label: `eval/${id}`, createdAt, status: "complete", verified: true, notes: [],
    versions: { suite, suiteHash: hash, snapshotDate: null, dataFingerprint: null, dataFingerprintSource: null, promptVersion: null, promptSnapshot: false, commit: null, dirty: null, binary: null, sourceFingerprint: null, grader: null, models: [] },
    original: { passed: 0, recorded: 0, expected: null, failed: 0, dataErrors: 0, executionRate: null, allPassedCases: 0, caseCount: 0, allPassedRate: null, repeats: null, usage: null, durationMs: 0 },
    current: null, regradedTrials: null,
  };
}

function entry(id: string, hash: string | null): TimelineEntry {
  return { run: run(id, "v1.5", "a".repeat(64)), frozenCaseCount: 50, commit: { hash, subject: "相同标题", committedAt: "2026-09-09T12:00:00+08:00", status: hash ? "available" : "unrecorded" } };
}

describe("saved evaluation catalogs", () => {
  it("version navigation deduplicates names while preserving diagnostics", () => {
    const runs = [run("one", "v1.5", "a".repeat(64)), run("two", "v1.5", "b".repeat(64)), run("three", "v1.5-dialogue-diagnostic", "c".repeat(64)), run("unknown", null, null)];
    expect(getSuiteVersions(runs)).toEqual(["v1.5-dialogue-diagnostic", "v1.5"]);
    expect(groupSuiteRuns(runs, "v1.5")).toHaveLength(2);
    expect(groupSuiteRuns(runs, UNRECORDED_SUITE_VERSION)[0].runs.map(item => item.id)).toEqual(["unknown"]);
  });

  it("same-name different-content suites remain separate and unknown digests never merge", () => {
    const runs = [run("same-a", "v1.5", "a".repeat(64)), run("same-b", "v1.5", "a".repeat(64)), run("modified", "v1.5", "b".repeat(64)), run("missing-a", "v1.5", null), run("missing-b", "v1.5", null), run("invalid-a", "v1.5", "short"), run("invalid-b", "v1.5", "short")];
    const groups = groupSuiteRuns(runs, "v1.5");
    expect(groups).toHaveLength(6);
    expect(groups.find(group => group.hash === "a".repeat(64))?.runs.map(item => item.id)).toEqual(["same-a", "same-b"]);
    expect(groups.filter(group => !group.hash).every(group => group.runs.length === 1)).toBe(true);
    expect(runs.map(item => item.id)).toEqual(["same-a", "same-b", "modified", "missing-a", "missing-b", "invalid-a", "invalid-b"]);
  });

  it("commits group by complete identity rather than matching titles or timestamps", () => {
    const { groups, unrecorded } = groupCommitRuns([entry("a", "a".repeat(40)), entry("b", "A".repeat(40)), entry("c", "b".repeat(40)), entry("missing-a", null), entry("missing-b", null), entry("short", "aaaaa")]);
    expect(groups).toHaveLength(2);
    expect(groups.find(group => group.hash === "a".repeat(40))?.entries.map(item => item.run.id)).toEqual(["a", "b"]);
    expect(unrecorded.map(item => item.run.id)).toEqual(["missing-a", "missing-b", "short"]);
  });

  it("metadata determines chronology including submillisecond precision; missing time stays unknown", () => {
    const runs = [run("20991231-filename", "v", null, null), run("before", "v", null, "2026-09-09T13:44:47.4133304+08:00"), run("after", "v", null, "2026-09-09T05:44:47.4133305Z")];
    expect(latestRuns(runs).map(item => item.id)).toEqual(["after", "before", "20991231-filename"]);
    expect(runs[0].createdAt).toBeNull();
  });
});
