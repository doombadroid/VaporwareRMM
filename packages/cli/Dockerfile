# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /build

COPY go.mod .
COPY main.go .

RUN CGO_ENABLED=0 GOOS=linux go build -o vaporrmm .

# Final stage
FROM alpine:latest

WORKDIR /app

COPY --from=builder /build/vaporrmm /app/

EXPOSE 8080

CMD ["./vaporrmm"]