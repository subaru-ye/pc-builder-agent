import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { Providers } from "./providers";
import { AuthForm } from "./auth-form";

const mocks = vi.hoisted(() => ({
  register: vi.fn(),
  login: vi.fn(),
  replace: vi.fn(),
  refresh: vi.fn(),
  broadcast: vi.fn(),
}));

vi.mock("@/lib/api/client", () => ({
  api: { register: mocks.register, login: mocks.login },
}));
vi.mock("@/lib/auth-events", () => ({ broadcastAuth: mocks.broadcast }));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace: mocks.replace, refresh: mocks.refresh }),
}));

describe("AuthForm", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("以正确的自动填充语义提交注册，并跳回安全的产品路径", async () => {
    mocks.register.mockResolvedValue({
      schema_version: 1,
      enabled: true,
      authenticated: true,
      claimed_session_count: 2,
      account: { id: "user-1", email: "learner@example.test", display_name: "装机新手", created_at: "2026-08-30T00:00:00Z" },
    });
    render(<Providers><AuthForm mode="register" nextPath="/s/session-1" /></Providers>);

    const name = screen.getByRole("textbox", { name: "显示名称" });
    const email = screen.getByRole("textbox", { name: "邮箱" });
    const password = screen.getByLabelText("密码", { selector: "input" });
    expect(name).toHaveAttribute("autocomplete", "name");
    expect(email).toHaveAttribute("autocomplete", "email");
    expect(password).toHaveAttribute("autocomplete", "new-password");

    await userEvent.type(name, "装机新手");
    await userEvent.type(email, "learner@example.test");
    await userEvent.type(password, "test-password-123");
    await userEvent.click(screen.getByRole("button", { name: "注册并认领会话" }));

    await waitFor(() => expect(mocks.register).toHaveBeenCalledOnce());
    expect(mocks.register).toHaveBeenCalledWith("learner@example.test", "test-password-123", "装机新手", expect.any(String));
    await waitFor(() => expect(mocks.replace).toHaveBeenCalledWith("/s/session-1"));
    expect(mocks.broadcast).toHaveBeenCalledWith("login");
  });

  it("登录密码使用 current-password，并可切换可见状态", async () => {
    render(<Providers><AuthForm mode="login" nextPath="/" /></Providers>);
    const password = screen.getByLabelText("密码", { selector: "input" });
    expect(password).toHaveAttribute("type", "password");
    expect(password).toHaveAttribute("autocomplete", "current-password");
    await userEvent.click(screen.getByRole("button", { name: "显示密码" }));
    expect(password).toHaveAttribute("type", "text");
  });
});
