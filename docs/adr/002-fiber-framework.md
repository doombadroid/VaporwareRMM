# ADR 002: Fiber Web Framework

## Status
Accepted

## Context
The server needs a fast, lightweight HTTP framework with middleware support, WebSocket capability, and good performance for high-throughput agent heartbeats.

## Decision
Use [gofiber/fiber](https://github.com/gofiber/fiber) v2.

## Consequences

### Positive
- Performance: ~10x faster than Gin, ~40x faster than standard library for routing
- Low memory footprint (uses fasthttp)
- Built-in middleware: CORS, logger, recover
- WebSocket support via `gofiber/websocket`
- Familiar Express-like API

### Negative
- fasthttp is not 100% compatible with `net/http` (some middleware needs adaptation)
- Smaller ecosystem than Gin or standard library
- Some third-party middleware only works with `net/http`

## Alternatives Considered
- **Standard library (`net/http`)**: maximum compatibility, but requires writing all middleware from scratch
- **Gin**: larger ecosystem, but slower than Fiber
- **Echo**: similar to Fiber, but slightly heavier
