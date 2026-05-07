FROM golang:1.23-alpine AS builder

WORKDIR /app

# dbus headers needed by fyne.io/systray (even with CGO=0 on Linux, dbus is a runtime dep)
# gcc/musl needed if CGO ever re-enabled
RUN apk add --no-cache gcc musl-dev dbus-dev

COPY packages/agent/go.mod packages/agent/go.sum ./
RUN go mod download

COPY packages/agent/ ./

RUN CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags="-s -w" -o /vaporrmm-agent .

# ── Runtime image ────────────────────────────────────────────
FROM alpine:3.21

# ca-certificates for HTTPS to server; dbus-libs for systray if display present
RUN apk --no-cache add ca-certificates dbus-libs

WORKDIR /app

COPY --from=builder /vaporrmm-agent /usr/local/bin/vaporrmm-agent

RUN chmod +x /usr/local/bin/vaporrmm-agent && \
    mkdir -p /etc/vaporrmm && chmod 750 /etc/vaporrmm

EXPOSE 47991

ENV VAPOR_SERVER_URL=http://server:8080
ENV VAPOR_AGENT_PORT=47991

# No DISPLAY set → agent auto-runs headless (systray skipped, see main.go)

ENTRYPOINT ["/usr/local/bin/vaporrmm-agent"]
