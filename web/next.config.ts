import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  devIndicators: false,
  // 离线浏览器验证可独立运行，避免占用用户正在使用的开发服务缓存。
  distDir: process.env.NEXT_DIST_DIR ?? ".next",
  outputFileTracingIncludes: {
    "/share/[token]/image": ["./node_modules/@pdfmergy-embedpdf/fonts-sc/fonts/NotoSansHans-Regular.otf"],
  },
  async rewrites() {
    const apiBase = process.env.GO_API_BASE_URL ?? "http://localhost:8082";
    // 本机评估服务显式启用，且只为本机 Host 代理；不接入生产 API。
    const evaldeskBase = process.env.EVALDESK_API_BASE_URL;
    return [
      ...(evaldeskBase ? [{
        source: "/api/evaldesk/:path*",
        destination: `${evaldeskBase}/api/evaldesk/:path*`,
        has: [{ type: "host" as const, value: "(?:localhost|127\\.0\\.0\\.1|\\[::1\\])" }],
      }] : []),
      { source: "/api/:path*", destination: `${apiBase}/api/:path*` },
      { source: "/healthz", destination: `${apiBase}/healthz` },
      { source: "/readyz", destination: `${apiBase}/readyz` },
    ];
  },
  async headers() {
    return [{
      source: "/share/:path*",
      headers: [
        { key: "Referrer-Policy", value: "no-referrer" },
        { key: "X-Robots-Tag", value: "noindex, nofollow, noarchive" },
        { key: "Cache-Control", value: "no-store" },
      ],
    }];
  },
};

export default nextConfig;
