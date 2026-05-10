import type { NextConfig } from "next";

const securityHeaders = [
  { key: "X-Content-Type-Options", value: "nosniff" },
  { key: "X-Frame-Options", value: "DENY" },
  { key: "X-XSS-Protection", value: "1; mode=block" },
  { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
  { key: "Permissions-Policy", value: "camera=(), microphone=(), geolocation=()" },
  {
    key: "Content-Security-Policy",
    value: [
      "default-src 'self'",
      // Next.js requires unsafe-inline for styles and inline scripts during hydration
      "script-src 'self' 'unsafe-inline' 'unsafe-eval'",
      "style-src 'self' 'unsafe-inline'",
      // connect-src controls fetch/XHR/WebSocket. 'self' covers the
      // single-origin Caddy production path; the extra http(s)://localhost
      // and ws(s)://localhost entries unblock the docker-compose.local.yml
      // path where dashboard (:3000) and server (:8080) sit on different
      // origins. Without these the browser silently rejects the cross-origin
      // POST and the login form shows a generic 'Invalid email or password'.
      "connect-src 'self' http://localhost:8080 ws://localhost:8080 ws: wss:",
      "img-src 'self' data: blob:",
      "font-src 'self'",
      "object-src 'none'",
      "base-uri 'self'",
      "form-action 'self'",
      "frame-ancestors 'none'",
    ].join("; "),
  },
];

const nextConfig: NextConfig = {
  output: 'standalone',
  images: {
    unoptimized: true,
  },
  env: {
    NEXT_PUBLIC_API_URL: process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8080/api',
  },
  async headers() {
    return [
      {
        source: "/(.*)",
        headers: securityHeaders,
      },
    ];
  },
  // In dev / e2e, proxy /api/* to the Go backend so the browser sees same-origin.
  // In production behind Caddy, the reverse proxy already handles this.
  async rewrites() {
    const apiTarget = process.env.API_PROXY_TARGET;
    if (!apiTarget) return [];
    return [
      { source: '/api/:path*', destination: `${apiTarget}/:path*` },
      { source: '/health', destination: `${apiTarget.replace(/\/api\/?$/, '')}/health` },
      { source: '/ws', destination: `${apiTarget.replace(/\/api\/?$/, '')}/ws` },
    ];
  },
};

export default nextConfig;
