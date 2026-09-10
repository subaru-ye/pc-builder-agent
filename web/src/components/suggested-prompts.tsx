"use client";

import { ArrowUpLeft } from "lucide-react";
import { Button } from "./ui/button";
import { useHydrated } from "@/hooks/use-hydrated";

const examples = ["8000 元，2K 玩黑神话：悟空", "预算 6000，主要剪 4K 视频，尽量安静", "一万元游戏主机，机箱要小，显卡优先"];

export function SuggestedPrompts({ onSelect, centered = false }: { onSelect: (text: string) => void; centered?: boolean }) {
  const hydrated = useHydrated();
  return <div aria-label="建议输入" className={`mt-6 flex flex-wrap gap-2 ${centered ? "justify-center" : ""}`}>
    {examples.map((example) => <Button key={example} disabled={!hydrated} variant="outline" className="h-auto min-h-11 max-w-full whitespace-normal text-left text-sm" onClick={() => {
      onSelect(example);
      document.getElementById("message-composer")?.focus();
    }}>{example}<ArrowUpLeft size={14} className="shrink-0" aria-hidden="true" /></Button>)}
  </div>;
}
