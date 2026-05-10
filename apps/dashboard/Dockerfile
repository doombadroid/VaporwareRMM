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

# node:22-alpine ships only node + npm — no wget, no curl, and the
# previous shell-form 'node -e "fetch(...)"' was silently failing
# (probably shell quoting around the arrow functions). Use exec form
# with a TCP connect, which is the minimum the orchestrator needs to
# know: 'is something listening on :3000?'. Keeps the probe trivial
# and immune to dashboard route latency / 5xx flakes.
HEALTHCHECK --interval=15s --timeout=5s --start-period=60s --retries=8 \
    CMD ["node", "-e", "require('net').createConnection({host:'127.0.0.1',port:3000}).on('connect',function(){process.exit(0);}).on('error',function(){process.exit(1);});"]

CMD ["node", "apps/dashboard/server.js"]
