# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Install SQLite dependencies
RUN apk add --no-cache sqlite-dev

# Copy go.mod and go.sum
COPY packages/server/go.* ./

# Download dependencies
RUN go mod download

# Copy source code
COPY packages/server/ ./packages/server/
COPY packages/models/ ./packages/models/

# Build the application
RUN CGO_ENABLED=1 GOOS=linux go build -a -installsuffix cgo \
    -o server \
    ./packages/server/main.go

# Production stage
FROM alpine:latest

WORKDIR /app

# Install SQLite runtime and ca-certificates
RUN apk --no-cache add sqlite-libs ca-certificates

# Create non-root user
RUN addgroup -g 1001 -S vapor && \
    adduser -S vapor -u 1001 -G vapor

# Copy binary
COPY --from=builder --chown=vapor:vapor /app/server ./server
COPY --from=builder --chown=vapor:vapor /app/packages/server/db/ ./db/

EXPOSE 8080

USER vapor

CMD ["./server"]