import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { ImageResponse } from "next/og";
import { fetchPublicShare } from "@/lib/api/server";
import { categories, categoryLabels, statusLabel } from "@/lib/domain";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

let fontPromise: Promise<Buffer> | undefined;
function loadFont() {
  fontPromise ??= readFile(join(process.cwd(), "node_modules", "@pdfmergy-embedpdf", "fonts-sc", "fonts", "NotoSansHans-Regular.otf"));
  return fontPromise;
}

export async function GET(_request: Request, { params }: { params: Promise<{ token: string }> }) {
  const { token } = await params;
  const build = await fetchPublicShare(token);
  if (!build) return new Response(null, { status: 404, headers: { "Cache-Control": "no-store" } });
  const font = await loadFont();
  const byCategory = new Map(build.parts.map((part) => [part.category, part]));
  const visual = build.summary.overall_status === "pass" ? { mark: "✓", color: "#45a66b" } : build.summary.overall_status === "fail" ? { mark: "×", color: "#d85c66" } : { mark: "!", color: "#d39a45" };
  return new ImageResponse(<div style={{ width: "100%", height: "100%", display: "flex", flexDirection: "column", background: "#0b0c0f", color: "#f2f3f5", fontFamily: "Noto Sans SC", padding: "54px 62px" }}>
    <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-start", borderBottom: "1px solid #272b35", paddingBottom: 28 }}><div style={{ display: "flex", flexDirection: "column" }}><div style={{ display: "flex", fontSize: 22, color: "#a6abb6" }}>装机配置单 Agent · v{build.summary.version}</div><div style={{ display: "flex", marginTop: 10, fontSize: 48, fontWeight: 700 }}>¥{build.quote.total_cny}</div><div style={{ display: "flex", marginTop: 8, fontSize: 20, color: "#a6abb6" }}>{build.requirement.use_case.type === "gaming" ? "游戏" : build.requirement.use_case.type === "productivity" ? "生产力" : "通用"}配置 · 快照 {build.quote.snapshot_date}</div></div><div style={{ display: "flex", alignItems: "center", gap: 10, color: visual.color, fontSize: 24 }}><span>{visual.mark}</span><span>{statusLabel(build.summary.overall_status)}</span></div></div>
    <div style={{ display: "flex", flex: 1, gap: 42, paddingTop: 30 }}><div style={{ display: "flex", flex: 1, flexDirection: "column" }}>{categories.slice(0, 4).map((category) => <Part key={category} label={categoryLabels[category]} name={byCategory.get(category)?.name ?? "未选择"} />)}</div><div style={{ display: "flex", flex: 1, flexDirection: "column" }}>{categories.slice(4).map((category) => <Part key={category} label={categoryLabels[category]} name={byCategory.get(category)?.name ?? "未选择"} />)}</div></div>
    <div style={{ display: "flex", justifyContent: "space-between", borderTop: "1px solid #272b35", paddingTop: 18, color: "#858c9a", fontSize: 16 }}><span>快照参考价；下单前复核官方规格</span><span>{build.summary.intent_label}</span></div>
  </div>, { width: 1200, height: 630, fonts: [{ name: "Noto Sans SC", data: font, weight: 400 }], headers: { "Cache-Control": "no-store", "Content-Disposition": `inline; filename="pc-build-v${build.summary.version}.png"` } });
}

function Part({ label, name }: { label: string; name: string }) { return <div style={{ display: "flex", alignItems: "center", minHeight: 68, borderBottom: "1px solid #20232b" }}><span style={{ width: 104, color: "#858c9a", fontSize: 17 }}>{label}</span><span style={{ display: "block", maxWidth: 360, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", fontSize: 20 }}>{name}</span></div>; }
