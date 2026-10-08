export type PromptChange = { kind: "same" | "added" | "removed"; text: string };
export type PromptDiffBlock = { kind: "same" | "changed"; beforeLine: number; afterLine: number; before: string; after: string; parts: PromptChange[]; detailed: boolean };

export const promptLength = (text: string) => Array.from(text).length;

function merge(parts: PromptChange[]): PromptChange[] {
  const result: PromptChange[] = [];
  for (const part of parts) {
    if (!part.text) continue;
    const last = result.at(-1);
    if (last?.kind === part.kind) last.text += part.text;
    else result.push({ ...part });
  }
  return result;
}

// ponytail: Myers 搜索最多 50 万个位置；大幅重写退回替换区间，明确标注未细化，避免阻塞浏览器。
export function diffPromptText(before: string, after: string): { parts: PromptChange[]; detailed: boolean } {
  const a = Array.from(before), b = Array.from(after);
  let start = 0, end = 0;
  while (start < a.length && start < b.length && a[start] === b[start]) start++;
  while (end < a.length - start && end < b.length - start && a[a.length - end - 1] === b[b.length - end - 1]) end++;
  const prefix: PromptChange = { kind: "same", text: a.slice(0, start).join("") };
  const suffix: PromptChange = { kind: "same", text: end ? a.slice(-end).join("") : "" };
  const old = a.slice(start, a.length - end), next = b.slice(start, b.length - end);
  const max = old.length + next.length, offset = max + 1;
  const frontier = new Int32Array(2 * max + 3);
  const trace: Int32Array[] = [];
  let work = 0;
  for (let d = 0; d <= max; d++) {
    for (let k = -d; k <= d; k += 2) {
      if (++work > 500_000) return { parts: merge([prefix, { kind: "removed", text: old.join("") }, { kind: "added", text: next.join("") }, suffix]), detailed: false };
      let x = k === -d || (k !== d && frontier[offset + k - 1] < frontier[offset + k + 1]) ? frontier[offset + k + 1] : frontier[offset + k - 1] + 1;
      let y = x - k;
      while (x < old.length && y < next.length && old[x] === next[y]) { x++; y++; }
      frontier[offset + k] = x;
      if (x >= old.length && y >= next.length) {
        const reversed: PromptChange[] = [];
        for (let step = d; step > 0; step--) {
          const prev = trace[step - 1], delta = x - y;
          const at = (diagonal: number) => prev[diagonal + step - 1];
          const prevK = delta === -step || (delta !== step && at(delta - 1) < at(delta + 1)) ? delta + 1 : delta - 1;
          const prevX = at(prevK), prevY = prevX - prevK;
          while (x > prevX && y > prevY) { reversed.push({ kind: "same", text: old[--x] }); y--; }
          if (x === prevX) reversed.push({ kind: "added", text: next[--y] });
          else reversed.push({ kind: "removed", text: old[--x] });
        }
        while (x > 0 && y > 0) { reversed.push({ kind: "same", text: old[--x] }); y--; }
        return { parts: merge([prefix, ...reversed.reverse(), suffix]), detailed: true };
      }
    }
    trace.push(frontier.slice(offset - d, offset + d + 1));
  }
  throw new Error("提示词差异未完成");
}

// ponytail: 行级定位最多每侧 400 行；超过时对全文做有界字符对比。
export function promptDiff(before: string, after: string): PromptDiffBlock[] {
  const a = before.split("\n"), b = after.split("\n");
  if (a.length > 400 || b.length > 400) return [{ kind: before === after ? "same" : "changed", beforeLine: 1, afterLine: 1, before, after, ...diffPromptText(before, after) }];
  const table = Array.from({ length: a.length + 1 }, () => new Uint16Array(b.length + 1));
  for (let i = a.length - 1; i >= 0; i--) for (let j = b.length - 1; j >= 0; j--) table[i][j] = a[i] === b[j] ? table[i + 1][j + 1] + 1 : Math.max(table[i + 1][j], table[i][j + 1]);
  const blocks: PromptDiffBlock[] = [];
  let i = 0, j = 0;
  while (i < a.length || j < b.length) {
    const beforeLine = i + 1, afterLine = j + 1, old: string[] = [], next: string[] = [];
    const same = i < a.length && j < b.length && a[i] === b[j];
    if (same) {
      while (i < a.length && j < b.length && a[i] === b[j]) { old.push(a[i++]); next.push(b[j++]); }
    } else {
      while ((i < a.length || j < b.length) && !(i < a.length && j < b.length && a[i] === b[j])) {
        if (i < a.length && (j === b.length || table[i + 1][j] >= table[i][j + 1])) old.push(a[i++]);
        else next.push(b[j++]);
      }
    }
    // Keep line separators so applying the change reconstructs both originals exactly.
    const oldText = old.join("\n") + (old.length && i < a.length ? "\n" : "");
    const nextText = next.join("\n") + (next.length && j < b.length ? "\n" : "");
    blocks.push({ kind: oldText === nextText ? "same" : "changed", beforeLine, afterLine, before: oldText, after: nextText, ...diffPromptText(oldText, nextText) });
  }
  return blocks;
}
