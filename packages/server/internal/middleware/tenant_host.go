// Package middleware contains cross-cutting Fiber handlers.
//
// tenant_host resolves a tenant from the request Host header (subdomain) and
// stashes the result in c.Locals("host_tenant_id") / "host_tenant_name".
//
// This is a HINT, not an enforcement: authenticated requests still trust the
// JWT's tid claim. The hint is used by:
//   - public branding endpoint (so the login page renders the right tenant)
//   - signup pre-fill
//   - Caddy on-demand TLS "ask" endpoint
package middleware

import (
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"vaporrmm/server/internal/db"

	"github.com/gofiber/fiber/v2"
)

type cachedTenant struct {
	id   string
	name string
}

type slugCache struct {
	mu      sync.RWMutex
	entries map[string]cachedTenant
	loaded  time.Time
}

var cache = &slugCache{entries: map[string]cachedTenant{}}

// InvalidateSlugCache flushes the in-memory slug → tenant cache. Call after
// any tenant CRUD so subsequent requests see the new state immediately.
func InvalidateSlugCache() {
	cache.mu.Lock()
	cache.entries = map[string]cachedTenant{}
	cache.loaded = time.Time{}
	cache.mu.Unlock()
}

// reloadIfStale repopulates the cache when older than 60s.
func (c *slugCache) reloadIfStale() {
	c.mu.RLock()
	stale := time.Since(c.loaded) > 60*time.Second || len(c.entries) == 0
	c.mu.RUnlock()
	if !stale {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.loaded) <= 60*time.Second && len(c.entries) > 0 {
		return // another goroutine refreshed while we waited
	}
	rows, err := db.DB.Query(`SELECT id, name, COALESCE(slug,'') FROM tenants WHERE status = 'active' AND slug != ''`)
	if err != nil {
		slog.Warn("tenant slug cache reload failed", "error", err)
		return
	}
	defer rows.Close()
	fresh := map[string]cachedTenant{}
	for rows.Next() {
		var id, name, slug string
		if err := rows.Scan(&id, &name, &slug); err != nil {
			continue
		}
		fresh[strings.ToLower(slug)] = cachedTenant{id: id, name: name}
	}
	c.entries = fresh
	c.loaded = time.Now()
}

func (c *slugCache) lookup(slug string) (cachedTenant, bool) {
	c.reloadIfStale()
	c.mu.RLock()
	defer c.mu.RUnlock()
	t, ok := c.entries[strings.ToLower(slug)]
	return t, ok
}

// ExtractSubdomainSlug returns the leftmost label of the host when it sits
// underneath the configured base domain (env BASE_DOMAIN, e.g. "rmm.example.com").
// Returns "" for the base domain itself, "www", or unknown hosts.
func ExtractSubdomainSlug(host string) string {
	if host == "" {
		return ""
	}
	// Strip port if present
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	host = strings.ToLower(host)
	base := strings.ToLower(strings.TrimSpace(os.Getenv("BASE_DOMAIN")))
	if base == "" || host == base {
		return ""
	}
	if !strings.HasSuffix(host, "."+base) {
		return ""
	}
	prefix := strings.TrimSuffix(host, "."+base)
	if prefix == "" || prefix == "www" {
		return ""
	}
	// Reject if the prefix has further dots (we only support single-label tenants for now)
	if strings.Contains(prefix, ".") {
		return ""
	}
	return prefix
}

// ResolveTenantFromHost is best-effort: it never aborts the request, only
// annotates locals when the subdomain matches an active tenant slug.
func ResolveTenantFromHost() fiber.Handler {
	return func(c *fiber.Ctx) error {
		slug := ExtractSubdomainSlug(c.Hostname())
		if slug == "" {
			return c.Next()
		}
		if t, ok := cache.lookup(slug); ok {
			c.Locals("host_tenant_id", t.id)
			c.Locals("host_tenant_name", t.name)
			c.Locals("host_tenant_slug", slug)
		}
		return c.Next()
	}
}

// SlugIsActive reports whether the slug maps to an active tenant.
// Used by the Caddy on-demand TLS "ask" endpoint.
func SlugIsActive(slug string) bool {
	if slug == "" {
		return false
	}
	_, ok := cache.lookup(slug)
	return ok
}
