import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { PriceAvailabilityNotice } from "./price-availability";

describe("PriceAvailabilityNotice", () => {
  it("只对搜索平台报价显示库存边界", () => {
    const { rerender } = render(<PriceAvailabilityNotice value="confirmed_stock" />);
    expect(screen.queryByText(/不代表库存/)).not.toBeInTheDocument();
    rerender(<PriceAvailabilityNotice value="search_listing" />);
    expect(screen.getByText("搜索平台报价，不代表库存，购买前请核对")).toBeInTheDocument();
  });
});
