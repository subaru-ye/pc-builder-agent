import type { RunSummary, TimelineEntry } from "./types";

export const UNRECORDED_SUITE_VERSION = "__unrecorded_suite_version__";

export function getSuiteVersions(runs: RunSummary[]): string[] {
  return [...new Set(runs.flatMap(run => run.versions.suite ? [run.versions.suite] : []))]
    .sort((a, b) => b.localeCompare(a, "zh-CN", { numeric: true }));
}

// Dates come only from metadata. Retain sub-millisecond ordering when saved
// timestamps differ below the precision of JavaScript Date.
function recordedTime(value: string | null): [number, number] {
  if (!value) return [-Infinity, 0];
  const time = new Date(value).getTime();
  if (Number.isNaN(time)) return [-Infinity, 0];
  return [time, Number((value.match(/\.(\d+)/)?.[1] ?? "").slice(0, 9).padEnd(9, "0"))];
}

export function latestRuns(runs: RunSummary[]): RunSummary[] {
  return [...runs].sort((a, b) => {
    const [at, af] = recordedTime(a.createdAt), [bt, bf] = recordedTime(b.createdAt);
    if (at !== bt) return bt > at ? 1 : -1;
    return bf - af || a.id.localeCompare(b.id);
  });
}

export interface SuiteRunGroup { key: string; version: string | null; hash: string | null; runs: RunSummary[] }

export function groupSuiteRuns(runs: RunSummary[], version: string): SuiteRunGroup[] {
  const groups = new Map<string, SuiteRunGroup>();
  for (const run of latestRuns(runs)) {
    const suite = run.versions.suite;
    if (version === UNRECORDED_SUITE_VERSION ? !!suite : suite !== version) continue;
    const hash = run.versions.suiteHash && /^[a-f0-9]{64}$/i.test(run.versions.suiteHash) ? run.versions.suiteHash.toLowerCase() : null;
    // Missing fingerprints are always separate evidence, even for identical
    // names or timestamps. Navigation labels alone do not prove equality.
    const key = JSON.stringify([suite, hash ?? { run: run.id }]);
    const group = groups.get(key) ?? { key, version: suite, hash, runs: [] };
    group.runs.push(run);
    groups.set(key, group);
  }
  return [...groups.values()];
}

export interface CommitRunGroup { hash: string; entries: TimelineEntry[] }

export function groupCommitRuns(entries: TimelineEntry[]): { groups: CommitRunGroup[]; unrecorded: TimelineEntry[] } {
  const groups = new Map<string, CommitRunGroup>(), unrecorded: TimelineEntry[] = [];
  const byID = new Map(entries.map(entry => [entry.run.id, entry]));
  for (const run of latestRuns(entries.map(entry => entry.run))) {
    const entry = byID.get(run.id)!;
    const hash = entry.commit.hash;
    if (!hash || !/^[a-f0-9]{40}$/i.test(hash)) { unrecorded.push(entry); continue; }
    const identity = hash.toLowerCase();
    const group = groups.get(identity) ?? { hash: identity, entries: [] };
    group.entries.push(entry);
    groups.set(identity, group);
  }
  return { groups: [...groups.values()], unrecorded };
}
