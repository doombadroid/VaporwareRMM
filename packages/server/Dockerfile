FROM golang:1.25-alpine AS builder

WORKDIR /app

# SQLite needs gcc + musl headers for CGO; lib/pq is pure Go (no extra deps)
RUN apk add --no-cache gcc musl-dev sqlite-libs sqlite-dev git

# Cache dependency downloads before copying source
COPY packages/models/go.mod ./packages/models/
COPY packages/server/go.mod packages/server/go.sum ./packages/server/

RUN cd packages/models && go mod download
RUN cd packages/server && go mod download

# Copy source
COPY packages/server/ ./packages/server/
COPY packages/models/ ./packages/models/

RUN cd packages/server && \
    CGO_ENABLED=1 GOOS=linux \
    go build -a -trimpath -ldflags="-s -w -X main.buildVersion=$(git describe --tags --always 2>/dev/null || echo unknown)" -o /app/server .

# ── Runtime image ────────────────────────────────────────────
FROM alpine:3.21

WORKDIR /app

# sqlite + TLS roots; wget for healthcheck
RUN apk --no-cache add sqlite-libs ca-certificates wget

RUN addgroup -g 1001 -S vapor && \
    adduser  -S vapor -u 1001 -G vapor

COPY --from=builder --chown=vapor:vapor /app/server ./server

EXPOSE 8080

USER vapor

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

CMD ["./server"]
