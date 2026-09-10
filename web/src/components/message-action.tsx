"use client";

import type { ComponentProps } from "react";
import { Button } from "./ui/button";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "./ui/tooltip";

export function MessageAction({ label, ...props }: ComponentProps<typeof Button> & { label: string }) {
  return <TooltipProvider><Tooltip><TooltipTrigger asChild>
    <Button {...props} variant="ghost" size="icon-sm" aria-label={label} className="h-7 w-7 text-[var(--ink-subtle)] [@media(pointer:coarse)]:h-11 [@media(pointer:coarse)]:w-11" />
  </TooltipTrigger><TooltipContent>{label}</TooltipContent></Tooltip></TooltipProvider>;
}
