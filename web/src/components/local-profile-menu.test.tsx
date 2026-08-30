import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { LocalProfileMenu } from "./local-profile-menu";
import { Providers } from "./providers";

vi.mock("next/navigation", () => ({ useRouter: () => ({ push: vi.fn(), refresh: vi.fn() }) }));

describe("LocalProfileMenu", () => {
  it("展示匿名身份并将尚未实现的账号操作保持禁用", async () => {
    render(<Providers><LocalProfileMenu forceLocal /></Providers>);

    await userEvent.click(screen.getByRole("button", { name: "打开本地访客菜单" }));
    expect(screen.getByText("本地匿名模式")).toBeVisible();
    expect(screen.getAllByText("会话只属于当前浏览器身份").length).toBeGreaterThan(0);
    expect(screen.getByRole("menuitem", { name: /个人信息/ })).toHaveAttribute("aria-disabled", "true");
    expect(screen.getByRole("menuitem", { name: /设置/ })).toHaveAttribute("aria-disabled", "true");
    expect(screen.getByRole("menuitem", { name: /退出登录/ })).toHaveAttribute("aria-disabled", "true");
  });
});
