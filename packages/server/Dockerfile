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

# ── Agent builder ─────────────────────────────────────────────
# Builds the linux/amd64 agent binary that the server hands out
# from /download/agent-linux-amd64. CGO_ENABLED=0 produces a fully
# static binary that runs on any Linux distro (musl OR glibc) —
# the downloader has no idea what libc the target endpoint has,
# so portability beats the marginal perf of a CGO build. Matches
# the standalone packages/agent/Dockerfile.
FROM golang:1.25-alpine AS agent-builder

WORKDIR /app

# dbus headers needed by fyne.io/systray's import graph even with CGO=0.
RUN apk add --no-cache gcc musl-dev dbus-dev

COPY packages/agent/go.mod packages/agent/go.sum ./
RUN go mod download

COPY packages/agent/ ./

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /app/agent-linux-amd64 .

# ── Runtime image ────────────────────────────────────────────
FROM alpine:3.21

WORKDIR /app

# sqlite + TLS roots; wget for healthcheck
RUN apk --no-cache add sqlite-libs ca-certificates wget

RUN addgroup -g 1001 -S vapor && \
    adduser  -S vapor -u 1001 -G vapor

COPY --from=builder --chown=vapor:vapor /app/server ./server

# Agent binary served at /download/agent-linux-amd64. Mode 0644 is
# correct — the file is served by the Fiber handler (read-only),
# not executed by the server process. Endpoints that download it
# chmod +x on their own side.
RUN mkdir -p /opt/agents && chown vapor:vapor /opt/agents
COPY --from=agent-builder --chown=vapor:vapor --chmod=0644 /app/agent-linux-amd64 /opt/agents/linux-amd64

EXPOSE 8080

USER vapor

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

CMD ["./server"]
