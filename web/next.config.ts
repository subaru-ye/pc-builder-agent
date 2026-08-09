import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  async rewrites() {
    const apiBase = process.env.GO_API_BASE_URL ?? "http://localhost:8082";
    return [
      { source: "/api/:path*", destination: `${apiBase}/api/:path*` },
      { source: "/healthz", destination: `${apiBase}/healthz` },
      { source: "/readyz", destination: `${apiBase}/readyz` },
    ];
  },
};

export default nextConfig;
