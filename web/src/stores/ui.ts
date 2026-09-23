import { create } from "zustand";

// 右栏共享 inspector 的顶层 Tab:需求状态默认,配置详情按版本启用。
// 只保存当前会话的瞬时 UI,会话切换时由工作台重置为 requirement。
type WorkspaceTab = "requirement" | "build";
// 配置详情内部的二级区块。
type InspectorSection = "build" | "validation" | "versions";

interface UIState {
  workspaceTab: WorkspaceTab;
  inspectorSection: InspectorSection;
  diffFrom: number | null;
  diffTo: number | null;
  setWorkspaceTab: (value: WorkspaceTab) => void;
  setInspectorSection: (value: InspectorSection) => void;
  setDiff: (from: number, to: number) => void;
}

export const useUIStore = create<UIState>((set) => ({
  workspaceTab: "requirement",
  inspectorSection: "build",
  diffFrom: null,
  diffTo: null,
  setWorkspaceTab: (workspaceTab) => set({ workspaceTab }),
  setInspectorSection: (inspectorSection) => set({ inspectorSection }),
  setDiff: (diffFrom, diffTo) => set({ diffFrom, diffTo }),
}));

export function resetWorkspaceUI() {
  useUIStore.setState({ workspaceTab: "requirement", inspectorSection: "build", diffFrom: null, diffTo: null });
}
