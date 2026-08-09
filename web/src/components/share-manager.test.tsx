import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ShareManager, isLocalShareURL } from "./share-manager";

const apiMock = vi.hoisted(() => ({
  listShares: vi.fn(), createShare: vi.fn(), revokeShareByID: vi.fn(), revokeShareByToken: vi.fn(),
}));

vi.mock("@/lib/api/client", () => ({ api: apiMock, publicExportURL: (token: string) => `/public/${token}.md` }));
vi.mock("@/hooks/use-mobile", () => ({ useMobile: () => false }));

function renderManager() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(<QueryClientProvider client={client}><ShareManager sessionID="session-1" version={3} /></QueryClientProvider>);
}

describe("ShareManager", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMock.listShares.mockResolvedValue([]);
    apiMock.createShare.mockResolvedValue({ schema_version: 1, id: "share-1", version: 3, token: "A".repeat(43), url: `http://localhost:3000/share/${"A".repeat(43)}`, created_at: "2026-08-09T12:00:00Z", revoked_at: null });
    apiMock.revokeShareByToken.mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } });
  });

  it("creates, copies and revokes the current immutable version", async () => {
    renderManager();
    fireEvent.click(screen.getByRole("button", { name: "分享配置 v3" }));
    fireEvent.click(await screen.findByRole("button", { name: "创建当前版本分享" }));
    expect(await screen.findByDisplayValue(/localhost:3000\/share/)).toBeVisible();
    expect(screen.getByText(/只在本机/)).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "复制" }));
    await waitFor(() => expect(navigator.clipboard.writeText).toHaveBeenCalledOnce());
    fireEvent.click(screen.getByRole("button", { name: "撤销" }));
    await waitFor(() => expect(apiMock.revokeShareByToken).toHaveBeenCalled());
    expect(await screen.findByText("刚才的链接已失效。")).toBeVisible();
  });
});

describe("isLocalShareURL", () => {
  it.each(["http://localhost:3000/x", "http://127.0.0.1/x", "http://192.168.1.10/x", "http://172.20.0.2/x", "http://10.0.0.2/x", "http://[::1]/x"])("recognizes local URL %s", (value) => expect(isLocalShareURL(value)).toBe(true));
  it("keeps public hosts public", () => expect(isLocalShareURL("https://build.example.com/share/x")).toBe(false));
});
