# Build stage
FROM node:22-alpine AS builder

WORKDIR /app

# Copy package files
COPY pnpm-lock.yaml package.json ./
COPY turbo.json ./
COPY apps/dashboard ./apps/dashboard/
COPY packages/ui ./packages/ui/

# Install dependencies (including devDependencies for build)
RUN corepack enable pnpm && pnpm install --frozen-lockfile

# Build the application
RUN pnpm --filter @vaporrmm/dashboard build

# Production stage
FROM node:22-alpine AS runner

WORKDIR /app

ENV NODE_ENV=production

# Create non-root user
RUN addgroup -g 1001 -S nodejs && \
    adduser -S nextjs -u 1001 -G nodejs

# Copy built assets
COPY --from=builder --chown=nextjs:nodejs /app/apps/dashboard/.next ./apps/dashboard/.next
COPY --from=builder --chown=nextjs:nodejs /app/apps/dashboard/public ./apps/dashboard/public
COPY --from=builder --chown=nextjs:nodejs /app/apps/dashboard/node_modules ./apps/dashboard/node_modules

EXPOSE 3000

USER nextjs

CMD ["pnpm", "--filter=@vaporrmm/dashboard", "start"]