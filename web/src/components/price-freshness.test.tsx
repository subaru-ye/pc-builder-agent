import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { PriceFreshnessNotice, freshnessLabel } from "./price-freshness";

describe("PriceFreshnessNotice", () => {
  it("对 stale 显示强核价警告和最早观察日期", () => {
    render(<PriceFreshnessNotice value={{ overall: "stale", oldest_observed_date: "2026-08-01", max_age_days: 26, fresh_count: 0, aging_count: 0, stale_count: 8, unknown_count: 0 }} />);
    expect(screen.getByRole("status")).toHaveTextContent("价格快照已过期");
    expect(screen.getByText(/最早观察日期 2026-08-01/)).toHaveTextContent("距今 26 天");
  });

  it("对缺失元数据使用 unknown，不伪造日期", () => {
    render(<PriceFreshnessNotice />);
    expect(screen.getByRole("status")).toHaveTextContent("无法关联观察日期");
    expect(screen.queryByText(/最早观察日期/)).not.toBeInTheDocument();
    expect(freshnessLabel("aging")).toContain("可能已变化");
  });
});
