import createClient from "openapi-fetch";
import type { paths } from "./generated";
import type {
  BuildDiff,
  BuildSummary,
  BuildView,
  Problem,
  Readiness,
  RequirementSpec,
  Run,
  Session,
  SessionSummary,
  Share,
  ShareRecord,
  AuthState,
} from "./types";

const client = createClient<paths>({ baseUrl: "", credentials: "include" });

export class ApiError extends Error {
  constructor(public readonly problem: Problem) {
    super(problem.detail || problem.title);
  }
}

function unwrap<T>(result: { data?: T; error?: unknown; response: Response }): T {
  if (result.data !== undefined) return result.data;
  const fallback: Problem = {
    type: "/problems/internal_error",
    title: "请求没有成功",
    status: result.response.status || 500,
    code: "internal_error",
    request_id: result.response.headers.get("X-Request-ID") ?? "unknown",
  };
  throw new ApiError((result.error as Problem | undefined) ?? fallback);
}

export const api = {
  async authState(): Promise<AuthState> {
    return unwrap(await client.GET("/api/v1/auth/me"));
  },
  async register(email: string, password: string, displayName: string, key: string): Promise<AuthState> {
    return unwrap(await client.POST("/api/v1/auth/register", {
      params: { header: { "Idempotency-Key": key } },
      body: { schema_version: 1, email, password, display_name: displayName },
    }));
  },
  async login(email: string, password: string, key: string): Promise<AuthState> {
    return unwrap(await client.POST("/api/v1/auth/login", {
      params: { header: { "Idempotency-Key": key } },
      body: { schema_version: 1, email, password },
    }));
  },
  async logout(): Promise<void> {
    const result = await client.POST("/api/v1/auth/logout");
    if (!result.response.ok) unwrap(result as never);
  },
  async updateProfile(displayName: string, key: string): Promise<AuthState> {
    return unwrap(await client.PATCH("/api/v1/auth/profile", {
      params: { header: { "Idempotency-Key": key } },
      body: { schema_version: 1, display_name: displayName },
    }));
  },
  async changePassword(currentPassword: string, newPassword: string, confirmPassword: string, key: string): Promise<void> {
    const result = await client.POST("/api/v1/auth/password/change", {
      params: { header: { "Idempotency-Key": key } },
      body: { schema_version: 1, current_password: currentPassword, new_password: newPassword, confirm_password: confirmPassword },
    });
    if (!result.response.ok) unwrap(result as never);
  },
  async readiness(): Promise<Readiness> {
    const response = await fetch("/readyz", { credentials: "include", cache: "no-store" });
    const data = (await response.json()) as Readiness | Problem;
    if ("dependencies" in data) return data as Readiness;
    throw new ApiError(data);
  },
  async listSessions(): Promise<SessionSummary[]> {
    const result = await client.GET("/api/v1/sessions");
    return unwrap(result).sessions;
  },
  async createSession(key: string): Promise<Session> {
    return unwrap(await client.POST("/api/v1/sessions", {
      params: { header: { "Idempotency-Key": key } },
    }));
  },
  async getSession(id: string): Promise<Session> {
    return unwrap(await client.GET("/api/v1/sessions/{session_id}", { params: { path: { session_id: id } } }));
  },
  async sendMessage(id: string, text: string, key: string): Promise<Run> {
    return unwrap(await client.POST("/api/v1/sessions/{session_id}/messages", {
      params: { path: { session_id: id }, header: { "Idempotency-Key": key } },
      body: { schema_version: 1, text },
    }));
  },
  async replaceRequirement(id: string, value: RequirementSpec, key: string): Promise<RequirementSpec> {
    return unwrap(await client.PATCH("/api/v1/sessions/{session_id}/requirement", {
      params: { path: { session_id: id }, header: { "Idempotency-Key": key } },
      body: value,
    }));
  },
  async confirmRequirement(id: string, key: string): Promise<Run> {
    return unwrap(await client.POST("/api/v1/sessions/{session_id}/requirement/confirm", {
      params: { path: { session_id: id }, header: { "Idempotency-Key": key } },
    }));
  },
  async getRun(id: string): Promise<Run> {
    return unwrap(await client.GET("/api/v1/runs/{run_id}", { params: { path: { run_id: id } } }));
  },
  async listBuilds(id: string): Promise<BuildSummary[]> {
    const result = await client.GET("/api/v1/sessions/{session_id}/builds", { params: { path: { session_id: id } } });
    return unwrap(result).builds;
  },
  async getBuild(id: string, version: number): Promise<BuildView> {
    return unwrap(await client.GET("/api/v1/sessions/{session_id}/builds/{version}", {
      params: { path: { session_id: id, version } },
    }));
  },
  async getDiff(id: string, from: number, to: number): Promise<BuildDiff> {
    return unwrap(await client.GET("/api/v1/sessions/{session_id}/diff", {
      params: { path: { session_id: id }, query: { from, to } },
    }));
  },
  async createShare(id: string, version: number, key: string): Promise<Share> {
    return unwrap(await client.POST("/api/v1/sessions/{session_id}/builds/{version}/shares", {
      params: { path: { session_id: id, version }, header: { "Idempotency-Key": key } },
    }));
  },
  async listShares(id: string, version: number): Promise<ShareRecord[]> {
    const result = await client.GET("/api/v1/sessions/{session_id}/builds/{version}/shares", {
      params: { path: { session_id: id, version } },
    });
    return unwrap(result).shares;
  },
  async revokeShareByID(id: string, version: number, shareID: string, key: string): Promise<void> {
    const result = await client.DELETE("/api/v1/sessions/{session_id}/builds/{version}/shares/{share_id}", {
      params: { path: { session_id: id, version, share_id: shareID }, header: { "Idempotency-Key": key } },
    });
    if (!result.response.ok) unwrap(result as never);
  },
  async revokeShareByToken(token: string, key: string): Promise<void> {
    const result = await client.DELETE("/api/v1/shares/{token}", {
      params: { path: { token }, header: { "Idempotency-Key": key } },
    });
    if (!result.response.ok) unwrap(result as never);
  },
};

export const exportURL = (sessionID: string, version: number) =>
  `/api/v1/sessions/${encodeURIComponent(sessionID)}/builds/${version}/export.md`;

export const publicExportURL = (token: string) =>
  `/api/v1/public/shares/${encodeURIComponent(token)}/export.md`;
