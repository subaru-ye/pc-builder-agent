import { describe, expect, it } from "vitest";
import { diffPromptText, promptDiff, promptLength } from "./prompt-diff";

function check(before: string, after: string) {
  const blocks = promptDiff(before, after);
  expect(blocks.map(block => block.before).join("")).toBe(before);
  expect(blocks.map(block => block.after).join("")).toBe(after);
  for (const block of blocks) {
    expect(block.parts.filter(part => part.kind !== "added").map(part => part.text).join("")).toBe(block.before);
    expect(block.parts.filter(part => part.kind !== "removed").map(part => part.text).join("")).toBe(block.after);
  }
  return blocks;
}

describe("提示词内容差异", () => {
  it("长段落只标记实际修改的字词，保留上下文", () => {
    const context = "保持需求解析规则与逐字证据。".repeat(250);
    const diff = check(context + "允许删除已有配件。" + context, context + "禁止删除已有配件。" + context);
    expect(diff.flatMap(block => block.parts).filter(part => part.kind === "removed").map(part => part.text).join("")).toBe("允许");
    expect(diff.flatMap(block => block.parts).filter(part => part.kind === "added").map(part => part.text).join("")).toBe("禁止");
    expect(diff.every(block => block.detailed)).toBe(true);
  });

  it("空原文、末尾换行、重复内容与中文 emoji 均可还原", () => {
    for (const [before, after] of [["", ""], ["", "新增\n"], ["移除", ""], ["甲\n", "甲"], ["甲", "甲\n"], ["甲\n乙\n甲", "甲\n甲"], ["🙂预算7500", "😀预算8000"], ["相同", "相同"]]) check(before, after);
    expect(promptLength("😀中文")).toBe(3);
    expect(diffPromptText("😀", "🙂").parts).toEqual([{ kind: "removed", text: "😀" }, { kind: "added", text: "🙂" }]);
    expect(promptDiff("甲\n", "甲")[0].kind).toBe("changed");
  });

  it("小输入的改动数与最短编辑距离一致", () => {
    const alphabet = ["", "甲", "乙", "甲甲", "甲乙", "乙甲", "甲乙甲", "\n", "甲\n乙"];
    for (const a of alphabet) for (const b of alphabet) {
      const table = Array.from({ length: a.length + 1 }, () => new Array<number>(b.length + 1).fill(0));
      for (let i = a.length - 1; i >= 0; i--) for (let j = b.length - 1; j >= 0; j--) table[i][j] = a[i] === b[j] ? 1 + table[i + 1][j + 1] : Math.max(table[i + 1][j], table[i][j + 1]);
      const diff = diffPromptText(a, b);
      expect(diff.parts.filter(part => part.kind !== "same").reduce((sum, part) => sum + promptLength(part.text), 0)).toBe(a.length + b.length - 2 * table[0][0]);
      check(a, b);
    }
  });

  it("超过行上限仍能定位小改动；大幅重写有界退化且不丢原文", () => {
    const lines = "保留内容\n".repeat(500);
    expect(check(lines + "甲", lines + "乙").every(block => block.detailed)).toBe(true);
    const large = check("甲".repeat(2000), "乙".repeat(2000));
    expect(large[0].detailed).toBe(false);
  });
});
