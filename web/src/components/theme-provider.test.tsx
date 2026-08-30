import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ThemeProvider, useTheme } from "./theme-provider";

let systemDark = false;
let systemListener: (() => void) | undefined;

function ThemeProbe() {
  const { theme, resolvedTheme, setTheme } = useTheme();
  return <div>
    <output aria-label="主题状态">{theme}/{resolvedTheme}</output>
    <button onClick={() => setTheme("system")}>系统</button>
    <button onClick={() => setTheme("dark")}>深色</button>
    <button onClick={() => setTheme("light")}>浅色</button>
  </div>;
}

describe("ThemeProvider", () => {
  beforeEach(() => {
    window.localStorage.clear();
    document.documentElement.classList.remove("dark", "light");
    systemDark = false;
    systemListener = undefined;
    Object.defineProperty(window, "matchMedia", {
      configurable: true,
      value: vi.fn(() => ({
        get matches() { return systemDark; },
        media: "(prefers-color-scheme: dark)",
        onchange: null,
        addEventListener: (_: string, listener: () => void) => { systemListener = listener; },
        removeEventListener: vi.fn(),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn(),
      })),
    });
  });

  it("持久化显式主题并立即更新根节点", async () => {
    render(<ThemeProvider><ThemeProbe /></ThemeProvider>);
    expect(screen.getByLabelText("主题状态")).toHaveTextContent("system/light");
    expect(document.documentElement).toHaveClass("light");

    await userEvent.click(screen.getByRole("button", { name: "深色" }));
    expect(screen.getByLabelText("主题状态")).toHaveTextContent("dark/dark");
    expect(document.documentElement).toHaveClass("dark");
    expect(window.localStorage.getItem("pcb-theme")).toBe("dark");
  });

  it("系统模式跟随操作系统主题变化", () => {
    render(<ThemeProvider><ThemeProbe /></ThemeProvider>);
    systemDark = true;
    act(() => systemListener?.());
    expect(screen.getByLabelText("主题状态")).toHaveTextContent("system/dark");
    expect(document.documentElement).toHaveClass("dark");
  });
});
