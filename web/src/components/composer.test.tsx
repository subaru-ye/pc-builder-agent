import { fireEvent, render, screen, within } from "@testing-library/react";
import type { ComponentProps } from "react";
import { describe, expect, it, vi } from "vitest";
import { Composer } from "./composer";
import { TooltipProvider } from "./ui/tooltip";

function renderComposer(props: ComponentProps<typeof Composer>) {
  return render(<TooltipProvider><Composer {...props} /></TooltipProvider>);
}

describe("Composer", () => {
  it("把发送操作放在输入框下方的中文工具区", () => {
    renderComposer({ value: "8000 元游戏主机", onChange: vi.fn(), onSend: vi.fn() });

    const input = screen.getByLabelText("输入需求或改单内容");
    const toolbar = screen.getByRole("group", { name: "输入操作" });
    expect(input.compareDocumentPosition(toolbar) & Node.DOCUMENT_POSITION_FOLLOWING).not.toBe(0);
    expect(within(toolbar).getByText("Enter 发送 · Shift+Enter 换行")).toBeInTheDocument();
    expect(within(toolbar).getByLabelText("已输入 10 个字符，共可输入 4000 个字符")).toBeInTheDocument();
    expect(within(toolbar).getByRole("button", { name: "发送" })).toBeEnabled();
  });

  it("保留 Enter 发送、Shift+Enter 换行和中文输入法保护", () => {
    const onSend = vi.fn();
    renderComposer({ value: "需要一台电脑", onChange: vi.fn(), onSend });
    const input = screen.getByLabelText("输入需求或改单内容");

    fireEvent.compositionStart(input);
    fireEvent.keyDown(input, { key: "Enter" });
    fireEvent.compositionEnd(input);
    fireEvent.keyDown(input, { key: "Enter", shiftKey: true });
    expect(onSend).not.toHaveBeenCalled();

    fireEvent.keyDown(input, { key: "Enter" });
    expect(onSend).toHaveBeenCalledTimes(1);
  });
});
