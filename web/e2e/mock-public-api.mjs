import { createServer } from "node:http";

const token = "P".repeat(43);
const categories = ["cpu", "gpu", "motherboard", "memory", "ssd", "psu", "case", "cooler"];
const rules = ["SOCKET_MATCH", "CHIPSET_SUPPORT", "MEMORY_GENERATION", "MEMORY_SPEED", "GPU_CLEARANCE", "COOLER_CLEARANCE", "PSU_HEADROOM", "FORM_FACTOR_SUPPORT", "M2_SLOT_CAPACITY", "GPU_POWER_CONNECTORS", "DISPLAY_OUTPUT", "COOLER_THERMAL_CAPACITY"];
const payload = {
  schema_version: 1,
  summary: { schema_version: 1, version: 3, parent_version: 2, intent_label: "更换配件", total_cny: "7499.00", snapshot_date: "2026-08-09", overall_status: "review", created_at: "2026-08-09T12:00:00Z" },
  requirement: { budget_cny: "7500.00", budget_flex_percent: 10, use_case: { type: "gaming", titles: ["黑神话：悟空"], resolution: "2K", fps_target: 60 }, size_pref: "any", noise_pref: "normal", brand_pref: { cpu: "amd", gpu: "amd" }, existing_parts: [], priority: ["gpu"] },
  parts: categories.map((category, index) => ({ category, sku: `sku-${index}`, name: `${category.toUpperCase()} 中文测试型号`, quantity: 1, unit_price_cny: "900.00", subtotal_cny: "900.00", rationale: "满足目标分辨率并保留升级空间" })),
  quote: { snapshot_date: "2026-08-09", total_cny: "7499.00", budget_cny: "7500.00", budget_delta_cny: "1.00", missing_count: 0, missing_skus: [] },
  validation: { overall_status: "review", checks: rules.map((rule_id, index) => ({ rule_id, outcome: index === 3 ? "unknown" : "pass", severity: "none", missing_fields: index === 3 ? ["memory.speed"] : [], detail: index === 3 ? "数据不足，需要复核内存频率" : "检查通过" })) },
  disclaimers: ["报价为 2026-08-09 快照参考价，非实时价格。", "兼容性结论基于本库收录参数，下单前请以官方规格页复核。", "本文档不构成购买建议。"],
  share: { created_at: "2026-08-09T12:30:00Z" },
};

createServer((request, response) => {
  const pathname = new URL(request.url ?? "/", "http://127.0.0.1").pathname;
  if (pathname === "/healthz") { response.writeHead(200, { "Content-Type": "application/json" }); response.end('{"status":"ok"}'); return; }
  if (pathname === `/api/v1/public/shares/${token}`) { response.writeHead(200, { "Content-Type": "application/json", "Cache-Control": "no-store" }); response.end(JSON.stringify(payload)); return; }
  if (pathname === `/api/v1/public/shares/${token}/export.md`) { response.writeHead(200, { "Content-Type": "text/markdown; charset=utf-8", "Content-Disposition": 'attachment; filename="pc-build-v3-2026-08-09.md"' }); response.end("# 装机配置单 v3\n"); return; }
  response.writeHead(404, { "Content-Type": "application/problem+json" }); response.end('{"code":"not_found"}');
}).listen(18082, "127.0.0.1");
