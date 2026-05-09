FROM node:22-alpine AS builder

WORKDIR /app

# NEXT_PUBLIC_API_URL is baked into the JS bundle at build time.
# When running behind Caddy (docker-compose), set this to /api.
# For standalone server access set to http://<host>:8080/api.
ARG NEXT_PUBLIC_API_URL=/api
ENV NEXT_PUBLIC_API_URL=$NEXT_PUBLIC_API_URL

# Copy workspace manifests for dependency caching
COPY pnpm-lock.yaml pnpm-workspace.yaml package.json turbo.json ./
COPY apps/dashboard/package.json ./apps/dashboard/

RUN corepack enable pnpm && pnpm install --frozen-lockfile

# Copy full dashboard source and build
COPY apps/dashboard/ ./apps/dashboard/
RUN pnpm --filter @vaporrmm/dashboard build

# ── Runtime image ────────────────────────────────────────────
FROM node:22-alpine AS runner

WORKDIR /app

ENV NODE_ENV=production
ENV PORT=3000
ENV HOSTNAME=0.0.0.0

RUN addgroup -g 1001 -S nodejs && \
    adduser  -S nextjs -u 1001 -G nodejs

COPY --from=builder --chown=nextjs:nodejs /app/apps/dashboard/.next/standalone ./
COPY --from=builder --chown=nextjs:nodejs /app/apps/dashboard/.next/static      ./apps/dashboard/.next/static
COPY --from=builder --chown=nextjs:nodejs /app/apps/dashboard/public             ./apps/dashboard/public

EXPOSE 3000

USER nextjs

# Cold Node + Next standalone init can take 30+s on smaller runners
# before /, /login etc. respond. Bumped start-period 15s->60s and
# retries 3->8 so a slow CI runner doesn't trip the gate before
# the server is genuinely listening.
HEALTHCHECK --interval=15s --timeout=5s --start-period=60s --retries=8 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:3000/ || exit 1

CMD ["node", "apps/dashboard/server.js"]
