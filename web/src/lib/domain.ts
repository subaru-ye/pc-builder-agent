import type { PartCategory } from "@/lib/api/types";

export const categories: PartCategory[] = ["cpu", "gpu", "motherboard", "memory", "ssd", "psu", "case", "cooler"];

export const categoryLabels: Record<PartCategory, string> = {
  cpu: "处理器", gpu: "显卡", motherboard: "主板", memory: "内存",
  ssd: "固态硬盘", psu: "电源", case: "机箱", cooler: "散热器",
};

export const ruleOrder = [
  "SOCKET_MATCH", "CHIPSET_SUPPORT", "MEMORY_GENERATION", "MEMORY_SPEED",
  "GPU_CLEARANCE", "COOLER_CLEARANCE", "PSU_HEADROOM", "FORM_FACTOR_SUPPORT",
  "M2_SLOT_CAPACITY", "GPU_POWER_CONNECTORS", "DISPLAY_OUTPUT", "COOLER_THERMAL_CAPACITY",
] as const;

export const ruleLabels: Record<string, string> = {
  SOCKET_MATCH: "处理器与主板接口", CHIPSET_SUPPORT: "芯片组支持", MEMORY_GENERATION: "内存代际",
  MEMORY_SPEED: "内存频率", GPU_CLEARANCE: "显卡长度空间", COOLER_CLEARANCE: "散热器空间",
  PSU_HEADROOM: "电源余量", FORM_FACTOR_SUPPORT: "主板与电源安装", M2_SLOT_CAPACITY: "M.2 插槽容量",
  GPU_POWER_CONNECTORS: "显卡供电接口", DISPLAY_OUTPUT: "显示输出", COOLER_THERMAL_CAPACITY: "散热能力",
};

export const phaseLabels: Record<string, string> = {
  collecting: "收集需求", requirement_ready: "等待确认", building: "生成配置",
  ready: "配置就绪", changing: "正在改单", error: "需要处理",
};

export const stageLabels: Record<string, string> = {
  screening: "正在整理需求", remote_processing: "正在生成并校验配置", finalizing: "正在保存结果",
};

export function statusLabel(status: string) {
  return status === "pass" ? "通过" : status === "fail" ? "未通过" : status === "review" ? "需复核" : "数据不足";
}
