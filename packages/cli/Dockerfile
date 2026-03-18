# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /build

# Install dependencies
RUN apk add --no-cache git

# Copy go.mod and go.sum
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo \
    -ldflags="-w -s" \
    -o vaporrmm-cli .

# Final stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=builder /build/vaporrmm-cli .
COPY --from=builder /build/README.md .

EXPOSE 8080

ENTRYPOINT ["./vaporrmm-cli"]
CMD ["--help"]

LABEL org.opencontainers.image.title="Vapor RMM CLI" \
      org.opencontainers.image.description="Command-line interface for Vapor RMM" \
      org.opencontainers.image.version="0.1.0"