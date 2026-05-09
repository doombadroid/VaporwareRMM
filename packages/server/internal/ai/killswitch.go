package ai

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/redis"
)

// Kill-switch precedence is conjunctive: a request runs only when ALL of
//   1. the global switch is NOT killed
//   2. the per-tenant switch is NOT killed
//   3. the per-capability switch is NOT killed
//   4. the per-(tenant,capability) switch is NOT killed
// are true. Missing rows default to NOT-killed (enabled=0). Plan locks the
// rest of the AI surface to default-off via separate flags (tenants.ai_enabled
// and ai_capability_tenant_config.enabled) so the kill switch is a pure
// override, not the only thing standing between off and on.

const (
	scopeGlobal = "global"
	pubsubTopic = "ai:kill"
	pollEvery   = 30 * time.Second
)

type killCache struct {
	mu      sync.RWMutex
	flags   map[string]bool // scope → killed
	loaded  bool
	lastErr error
}

var cache = &killCache{flags: map[string]bool{}}

// scopeTenant returns the lookup key for a per-tenant kill switch.
func scopeTenant(tenantID string) string { return "tenant:" + tenantID }

// scopeCap returns the lookup key for a per-capability kill switch.
func scopeCap(capabilityID string) string { return "capability:" + capabilityID }

// scopeTenantCap returns the most specific key.
func scopeTenantCap(tenantID, capabilityID string) string {
	return "tenant:" + tenantID + ":capability:" + capabilityID
}

// IsKilled returns true if any of the relevant scopes are killed. The empty
// string for capabilityID skips that level (used when checking provider-only
// calls that aren't tied to a capability yet).
func IsKilled(tenantID, capabilityID string) bool {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	if cache.flags[scopeGlobal] {
		return true
	}
	if cache.flags[scopeTenant(tenantID)] {
		return true
	}
	if capabilityID != "" {
		if cache.flags[scopeCap(capabilityID)] {
			return true
		}
		if cache.flags[scopeTenantCap(tenantID, capabilityID)] {
			return true
		}
	}
	return false
}

// SetKill flips a scope on or off + persists + broadcasts. Caller is
// responsible for authorising the change (only super_admin can flip global,
// only tenant_admin or super_admin can flip per-tenant, etc.).
func SetKill(scope string, killed bool, reason, setByUserID string) error {
	now := time.Now().Unix()
	val := 0
	if killed {
		val = 1
	}
	// Upsert. We use an INSERT ... ON CONFLICT to keep set_at fresh on every
	// flip. Postgres-only here (we already gated on dialect upstream).
	_, err := db.DB.Exec(`
		INSERT INTO ai_kill_switches (scope, enabled, reason, set_by_user_id, set_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (scope) DO UPDATE
		   SET enabled=EXCLUDED.enabled, reason=EXCLUDED.reason,
		       set_by_user_id=EXCLUDED.set_by_user_id, set_at=EXCLUDED.set_at
	`, scope, val, reason, setByUserID, now)
	if err != nil {
		return fmt.Errorf("ai: persist kill switch: %w", err)
	}
	cache.mu.Lock()
	cache.flags[scope] = killed
	cache.mu.Unlock()
	// Best-effort broadcast; if Redis is down, the next poll will catch up.
	if redis.IsEnabled() {
		_ = redis.Client.Publish(redis.Ctx, pubsubTopic, scope).Err()
	}
	return nil
}

// LoadAll re-reads every kill switch row into the cache. Called at boot, after
// any explicit SetKill, and periodically as the Redis-down fallback.
func LoadAll(ctx context.Context) error {
	rows, err := db.DB.Query(`SELECT scope, enabled FROM ai_kill_switches`)
	if err != nil {
		cache.mu.Lock()
		cache.lastErr = err
		cache.mu.Unlock()
		return err
	}
	defer rows.Close()
	flags := map[string]bool{}
	for rows.Next() {
		var scope string
		var enabled int
		if err := rows.Scan(&scope, &enabled); err != nil {
			slog.Warn("ai killswitch scan failed", "error", err)
			continue
		}
		flags[scope] = enabled == 1
	}
	cache.mu.Lock()
	cache.flags = flags
	cache.loaded = true
	cache.lastErr = nil
	cache.mu.Unlock()
	return nil
}

// StartKillSwitchWatcher starts the background sync loop. Subscribes to
// Redis pub/sub for instant updates and falls back to polling every 30s when
// Redis is unreachable so the cache cannot drift indefinitely.
func StartKillSwitchWatcher(ctx context.Context) {
	if err := SupportedDialect(); err != nil {
		// AI features disabled; nothing to watch.
		return
	}
	if err := LoadAll(ctx); err != nil {
		slog.Warn("ai killswitch initial load failed", "error", err)
	}
	go func() {
		ticker := time.NewTicker(pollEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := LoadAll(ctx); err != nil {
					slog.Warn("ai killswitch poll failed", "error", err)
				}
			}
		}
	}()
	if redis.IsEnabled() {
		go func() {
			ps := redis.Client.Subscribe(redis.Ctx, pubsubTopic)
			defer ps.Close()
			ch := ps.Channel()
			for {
				select {
				case <-ctx.Done():
					return
				case msg, ok := <-ch:
					if !ok {
						return
					}
					// One scope changed; cheapest correct response is to
					// reload everything. We do this rarely, latency doesn't
					// matter, and we avoid a stale-cache parse-the-message bug.
					_ = msg
					if err := LoadAll(ctx); err != nil {
						slog.Warn("ai killswitch reload after pubsub failed", "error", err)
					}
				}
			}
		}()
	}
}
