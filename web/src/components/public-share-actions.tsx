"use client";

import { Check, Copy, Download, ImageDown } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { publicExportURL } from "@/lib/api/client";
import { Button } from "./ui/button";

export function PublicShareActions({ token, version }: { token: string; version: number }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    await navigator.clipboard.writeText(window.location.href);
    setCopied(true);
    toast.success("链接已复制");
    window.setTimeout(() => setCopied(false), 1800);
  };
  const downloadImage = async () => {
    try {
      const response = await fetch(`/share/${encodeURIComponent(token)}/image`, { cache: "no-store" });
      if (!response.ok) throw new Error("image unavailable");
      const href = URL.createObjectURL(await response.blob());
      const anchor = document.createElement("a");
      anchor.href = href; anchor.download = `pc-build-v${version}.png`; anchor.click();
      URL.revokeObjectURL(href);
    } catch { toast.error("分享图下载失败，请稍后重试"); }
  };
  return <div className="public-actions flex flex-wrap gap-2">
    <Button className="h-11" onClick={copy}>{copied ? <Check /> : <Copy />}{copied ? "已复制" : "复制链接"}</Button>
    <Button asChild variant="outline" className="h-11"><a href={publicExportURL(token)}><Download />下载 Markdown</a></Button>
    <Button variant="outline" className="h-11" onClick={downloadImage}><ImageDown />下载分享图</Button>
  </div>;
}
