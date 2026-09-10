import { create } from "zustand";

type MobilePane = "chat" | "build";
type InspectorTab = "build" | "validation" | "versions";

interface UIState {
  mobilePane: MobilePane;
  inspectorTab: InspectorTab;
  diffFrom: number | null;
  diffTo: number | null;
  setMobilePane: (value: MobilePane) => void;
  setInspectorTab: (value: InspectorTab) => void;
  setDiff: (from: number, to: number) => void;
}

export const useUIStore = create<UIState>((set) => ({
  mobilePane: "chat",
  inspectorTab: "build",
  diffFrom: null,
  diffTo: null,
  setMobilePane: (mobilePane) => set({ mobilePane }),
  setInspectorTab: (inspectorTab) => set({ inspectorTab }),
  setDiff: (diffFrom, diffTo) => set({ diffFrom, diffTo }),
}));
