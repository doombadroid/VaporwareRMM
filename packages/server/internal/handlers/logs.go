package handlers

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"vaporrmm/server/internal/auth"

	"github.com/gofiber/fiber/v2"
	"github.com/valyala/fasthttp"
)

// logBuffer is a small in-memory ring buffer that captures slog output.
// Lets the SSE endpoint replay recent lines on connect so an admin
// connecting fresh sees context, not just future lines.
//
// Multi-server caveat: each instance has its own buffer. SSE clients
// only see the lines emitted by the instance handling their request.
// Centralized log shipping (Stage 16+) is a separate concern.
type logBuffer struct {
	mu      sync.Mutex
	entries []string
	cap     int
	subs    map[chan string]struct{}
}

// LogTap is the global capture point. Initialized in main via InstallLogTap.
var LogTap = newLogBuffer(2000)

func newLogBuffer(cap int) *logBuffer {
	return &logBuffer{
		entries: make([]string, 0, cap),
		cap:     cap,
		subs:    map[chan string]struct{}{},
	}
}

// write appends a line + fans it out to subscribers. Non-blocking — if
// a slow subscriber's channel is full, drop the line for that
// subscriber rather than block the writer.
func (b *logBuffer) write(line string) {
	line = redactLogLine(line)
	b.mu.Lock()
	if len(b.entries) >= b.cap {
		b.entries = b.entries[1:]
	}
	b.entries = append(b.entries, line)
	for ch := range b.subs {
		select {
		case ch <- line:
		default:
		}
	}
	b.mu.Unlock()
}

func (b *logBuffer) subscribe() chan string {
	ch := make(chan string, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *logBuffer) unsubscribe(ch chan string) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
	close(ch)
}

func (b *logBuffer) snapshot() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.entries))
	copy(out, b.entries)
	return out
}

// Redaction patterns for the SSE surface. Best-effort defense against a
// malformed log line containing a token; the real fix is to never log
// secrets in the first place. Order matters — we strip the secret value
// after the key=, leaving the key visible so the operator sees what
// kind of secret was redacted.
var (
	reJWT       = regexp.MustCompile(`(?i)(jwt[_-]?secret|JWT_SECRET)["\s:=]+["']?[^\s"',]+`)
	reBearer    = regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._\-]+`)
	rePassword  = regexp.MustCompile(`(?i)(password|passwd|pwd)["\s:=]+["']?[^\s"',]+`)
	reHexToken  = regexp.MustCompile(`\b[a-fA-F0-9]{32,}\b`)
	reSecretKey = regexp.MustCompile(`(?i)(api[_-]?key|secret[_-]?key|access[_-]?token|client[_-]?secret)["\s:=]+["']?[^\s"',]+`)
	reControl   = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]`)
)

// redactLogLine masks common credential patterns. Keep the leading key
// (e.g. `JWT_SECRET=`) so operators can debug WHICH secret leaked even
// though they can't see the value.
func redactLogLine(s string) string {
	s = reControl.ReplaceAllString(s, "")
	s = reJWT.ReplaceAllString(s, "$1=[REDACTED]")
	s = rePassword.ReplaceAllString(s, "$1=[REDACTED]")
	s = reSecretKey.ReplaceAllString(s, "$1=[REDACTED]")
	s = reBearer.ReplaceAllString(s, "Bearer [REDACTED]")
	s = reHexToken.ReplaceAllString(s, "[REDACTED-TOKEN]")
	return s
}

// InstallLogTap wires slog to also write into the in-memory buffer. We
// keep the existing handler (stderr) and chain a tee handler.
func InstallLogTap() {
	prev := slog.Default().Handler()
	slog.SetDefault(slog.New(&teeHandler{primary: prev, tap: LogTap}))
}

type teeHandler struct {
	primary slog.Handler
	tap     *logBuffer
}

func (t *teeHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return t.primary.Enabled(ctx, l)
}
func (t *teeHandler) Handle(ctx context.Context, r slog.Record) error {
	// Render record to a textual line. Don't try to mirror the primary
	// handler's format byte-for-byte; just give the SSE consumer
	// time + level + message + attrs, comma-separated.
	var b []byte
	b = append(b, r.Time.UTC().Format(time.RFC3339)...)
	b = append(b, ' ')
	b = append(b, r.Level.String()...)
	b = append(b, ' ')
	b = append(b, r.Message...)
	r.Attrs(func(a slog.Attr) bool {
		b = append(b, ' ')
		b = append(b, a.Key...)
		b = append(b, '=')
		b = append(b, fmt.Sprint(a.Value.Any())...)
		return true
	})
	t.tap.write(string(b))
	return t.primary.Handle(ctx, r)
}

func (t *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &teeHandler{primary: t.primary.WithAttrs(attrs), tap: t.tap}
}
func (t *teeHandler) WithGroup(name string) slog.Handler {
	return &teeHandler{primary: t.primary.WithGroup(name), tap: t.tap}
}

// RegisterLogRoutes wires the SSE log tail at /admin/logs/tail. The
// route is super_admin only — server logs cross tenants, so even
// tenant admins shouldn't see the firehose.
func RegisterLogRoutes(api fiber.Router) {
	api.Get("/admin/logs/recent", auth.SuperAdminMiddleware(), func(c *fiber.Ctx) error {
		lines := LogTap.snapshot()
		return c.JSON(fiber.Map{"lines": lines})
	})

	api.Get("/admin/logs/tail", auth.SuperAdminMiddleware(), func(c *fiber.Ctx) error {
		c.Set("Content-Type", "text/event-stream")
		c.Set("Cache-Control", "no-cache")
		c.Set("Connection", "keep-alive")
		c.Set("X-Accel-Buffering", "no") // disable nginx buffering

		ch := LogTap.subscribe()
		// Snapshot first so client gets context; then live stream.
		initial := LogTap.snapshot()

		c.Status(fiber.StatusOK)
		c.Context().SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {
			defer LogTap.unsubscribe(ch)

			// Replay buffer.
			for _, line := range initial {
				if _, err := fmt.Fprintf(w, "data: %s\n\n", line); err != nil {
					return
				}
			}
			if err := w.Flush(); err != nil {
				return
			}

			// Heartbeat keeps proxies from killing the connection.
			heartbeat := time.NewTicker(20 * time.Second)
			defer heartbeat.Stop()
			for {
				select {
				case line, ok := <-ch:
					if !ok {
						return
					}
					if _, err := fmt.Fprintf(w, "data: %s\n\n", line); err != nil {
						return
					}
					if err := w.Flush(); err != nil {
						return
					}
				case <-heartbeat.C:
					if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
						return
					}
					if err := w.Flush(); err != nil {
						return
					}
				}
			}
		}))
		return nil
	})
}
