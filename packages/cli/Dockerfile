# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /build

# Copy go.mod and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o vaporcli .

# Final stage
FROM alpine:latest

WORKDIR /app

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates

# Copy binary from builder
COPY --from=builder /build/vaporcli .

# Expose no ports (CLI doesn't need to expose ports)
EXPOSE 8080

ENTRYPOINT ["./vaporcli"]
CMD ["--help"]