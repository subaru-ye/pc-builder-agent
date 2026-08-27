export function PriceAvailabilityNotice({ value }: { value?: "confirmed_stock" | "search_listing" | "unknown" }) {
  if (value !== "search_listing") return null;
  return <p className="mt-1 text-xs status-review">搜索平台报价，不代表库存，购买前请核对</p>;
}
