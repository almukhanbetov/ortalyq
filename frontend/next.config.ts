import type { NextConfig } from "next";

// Адрес бэкенда для проксирования /api (dev и шаблон записи для Egress).
const backend = process.env.BACKEND_INTERNAL_URL || "http://127.0.0.1:18080";

const nextConfig: NextConfig = {
  output: "standalone",
  reactStrictMode: false,
  poweredByHeader: false,
  async rewrites() {
    return [{ source: "/api/:path*", destination: `${backend}/api/:path*` }];
  },
  async headers() {
    return [
      {
        source: "/:path*",
        headers: [
          { key: "X-Content-Type-Options", value: "nosniff" },
          { key: "Referrer-Policy", value: "same-origin" },
          { key: "Permissions-Policy", value: "camera=(self), microphone=(self), display-capture=(self)" },
        ],
      },
    ];
  },
};

export default nextConfig;
