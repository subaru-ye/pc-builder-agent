import { ApiError } from "./client";
import { userMessage } from "./problem";
import { describe, expect, it } from "vitest";

describe("userMessage", () => {
  it("maps product problem codes to actionable Chinese copy", () => {
    expect(userMessage(new ApiError({ type: "/problems/context_expired", title: "expired", status: 409, code: "context_expired", request_id: "r1" }))).toContain("整单生成");
  });

  it("does not parse server implementation details", () => {
    expect(userMessage(new ApiError({ type: "/problems/session_busy", title: "busy", detail: "sql: row locked", status: 409, code: "session_busy", request_id: "r2" }))).not.toContain("sql");
  });
});
