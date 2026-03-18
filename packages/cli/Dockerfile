# vaporRMM Agent Dockerfile
FROM golang:1.23-alpine AS builder

WORKDIR /build

# Install dependencies
RUN apk add --no-cache git

# Copy go.mod and go.sum
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the agent
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o vaporrmm-agent .

# Final stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy binary
COPY --from=builder /build/vaporrmm-agent .

# Create non-root user
RUN adduser -D -g '' appuser && \
    chown -R appuser:appuser /root
USER appuser

EXPOSE 47990

CMD ["./vaporrmm-agent"]