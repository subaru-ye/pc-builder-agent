import type { Metadata } from "next";
import { GeistSans } from "geist/font/sans";
import { GeistMono } from "geist/font/mono";
import Script from "next/script";
import { Providers } from "@/components/providers";
import "./globals.css";

export const metadata: Metadata = {
  title: "装机配置单 Agent",
  description: "从需求确认到兼容性校验的 DIY PC 配置工作台",
};

const themeScript = `(function(){try{var p=localStorage.getItem("pcb-theme");if(p!=="system"&&p!=="dark"&&p!=="light")p="system";var r=p==="system"?(matchMedia("(prefers-color-scheme: dark)").matches?"dark":"light"):p;var e=document.documentElement;e.classList.remove("dark","light");e.classList.add(r);e.dataset.theme=r;e.dataset.themePreference=p}catch(_){document.documentElement.classList.add("dark")}})()`;

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html lang="zh-CN" suppressHydrationWarning className={`${GeistSans.variable} ${GeistMono.variable} h-full antialiased`}>
      <head><Script id="theme-init" strategy="beforeInteractive">{themeScript}</Script></head>
      <body className="min-h-full"><Providers>{children}</Providers></body>
    </html>
  );
}
