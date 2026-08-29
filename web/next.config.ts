import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  devIndicators: false,
  outputFileTracingIncludes: {
    "/share/[token]/image": ["./node_modules/@pdfmergy-embedpdf/fonts-sc/fonts/NotoSansHans-Regular.otf"],
  },
  async rewrites() {
    const apiBase = process.env.GO_API_BASE_URL ?? "http://localhost:8082";
    return [
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
