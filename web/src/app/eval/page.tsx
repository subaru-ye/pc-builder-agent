import { Suspense } from "react";
import type { Metadata } from "next";
import { EvalWorkbench } from "@/components/evaldesk/workbench";

export const metadata: Metadata = {
  title: "本机评估 · 装机配置单 Agent",
  robots: { index: false, follow: false },
};

export default function EvalPage() {
  return <Suspense fallback={<p className="p-6" role="status">正在打开评估工作台…</p>}><EvalWorkbench /></Suspense>;
}
