import type { Session } from "./api/types";

type Result = NonNullable<Session["proposal"]>["result"];
export type Source = Result["evidence"][number];
export type SourceGroup = { key: string; url: string; title: string; used: boolean; entries: Source[] };

// Presentation grouping only: all original evidence stays in the server snapshot.
export function groupProposalSources(result: Result): SourceGroup[] {
  const refs = new Set((result.assessments ?? []).flatMap((a) => a.evidence));
  const selection = result.draft?.selection;
  const values = selection && typeof selection === "object" ? Object.values(selection).flatMap((v: unknown) => Array.isArray(v) ? v.map((item: { sku?: string }) => item.sku) : [v]) : result.candidates.map((c) => c.id);
  const selected = new Set(values.filter((v): v is string => typeof v === "string"));
  for (const c of result.candidates.filter((c) => selected.has(c.id))) {
    for (const id of [...c.evidence, ...Object.values(c.field_evidence ?? {})]) refs.add(id);
  }
  const groups = new Map<string, SourceGroup>();
  for (const source of result.evidence) {
    // Old catalog records used a stable "SKU · field · status" title.
    const candidate = source.candidate_id ?? (source.kind === "catalog" ? source.title.split(" · ")[0] : undefined);
    const used = refs.has(source.id) || (candidate !== undefined && selected.has(candidate));
    // A shared vendor page can cover unselected parts too. Keep those excerpts
    // in the secondary section even when the same URL supports a selected part.
    const key = `${used ? "used" : "other"}:${source.url || source.id}`;
    const group = groups.get(key) ?? { key, url: source.url, title: source.kind === "catalog" ? "商品规格与报价资料" : source.title, used: false, entries: [] };
    group.used ||= used;
    group.entries.push(source);
    groups.set(key, group);
  }
  return [...groups.values()];
}

export const sourceKindLabels: Record<string, string> = { catalog: "本地库已有资料", search: "已保存的搜索线索", page: "已读取的网页正文" };

export const sourceFieldLabels: Record<string, string> = {
  socket: "处理器插槽", tdp_w: "功耗", memory_type: "内存类型", memory_generation: "内存代际",
  memory_speed_max_mts: "支持内存频率", speed_mts: "内存频率", form_factor: "板型", chipset: "芯片组",
  gpu_length_max_mm: "显卡限长", length_mm: "长度", height_mm: "高度", cooler_height_max_mm: "散热器限高",
  wattage_w: "额定功率", m2_slots: "M.2 槽位", supported_sockets: "支持插槽", supported_form_factors: "支持板型",
  supported_psu_form_factors: "支持电源形态", psu_length_max_mm: "电源限长",
  cooling_capacity_w: "散热能力", price_cny: "参考报价", model: "型号", brand: "品牌", capacity_gb: "容量",
};

export function retrievalSummary(result: Result): string {
  if (result.search_calls === undefined || result.page_calls === undefined) return "历史检索记录，未记录本轮联网次数";
  if (result.search_calls === 0 && result.page_calls === 0) {
    return result.evidence.some((e) => e.kind !== "catalog") ? "本轮未联网，复用已保存资料" : "本轮未联网，使用本地资料";
  }
  return `本轮尝试搜索 ${result.search_calls} 次、读取网页 ${result.page_calls} 次`;
}
