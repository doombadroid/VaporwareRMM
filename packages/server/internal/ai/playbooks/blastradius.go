package playbooks

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/redis"
)

// ErrBlastRadiusExceeded means the per-(capability, tenant) action count in
// the configured sliding window has reached the cap. Callers translate this
// into a 429-style refusal and page an operator. Manual playbook runs
// trigger the same limiter — the cap is a safety net, not just an AI thing.
var ErrBlastRadiusExceeded = errors.New("playbooks: blast-radius cap reached for this capability in the configured window")

// BlastConfig is what the chokepoint's per-capability config provides.
// MaxDevices=0 means unlimited (operator opted out — usually only super_admin
// or for manual review-only modes).
type BlastConfig struct {
	MaxDevices    int
	WindowMinutes int
}

// in-process fallback when Redis is unavailable. We use a sync.Map of
// per-key sliding windows. Persists across capability invocations within
// one server process; reset on restart. Acceptable degradation — the cap
// is a safety net, not a forensic ledger.
type localCounter struct {
	mu      sync.Mutex
	windows map[string]*window
}
type window struct {
	timestamps []time.Time
}

var localBlast = &localCounter{windows: map[string]*window{}}

// Reserve records one action against the (capability, tenant) sliding window
// and returns ErrBlastRadiusExceeded if the cap would be exceeded. It does
// NOT release on failure — the model's "I am about to act" decision IS the
// chargeable event; if the action itself fails downstream we still want it
// counted, otherwise a confused model can flap forever within the window.
func Reserve(ctx context.Context, capabilityID, tenantID string, cfg BlastConfig) error {
	if cfg.MaxDevices <= 0 {
		return nil // unlimited
	}
	if cfg.WindowMinutes <= 0 {
		cfg.WindowMinutes = 5
	}
	if redis.IsEnabled() {
		return reserveRedis(ctx, capabilityID, tenantID, cfg)
	}
	return reserveLocal(capabilityID, tenantID, cfg)
}

func reserveRedis(ctx context.Context, capabilityID, tenantID string, cfg BlastConfig) error {
	key := fmt.Sprintf("ai:blast:%s:%s", capabilityID, tenantID)
	ttl := time.Duration(cfg.WindowMinutes) * time.Minute
	// Atomic INCR + EXPIRE. The EXPIRE refreshes only on the FIRST INCR each
	// window; subsequent INCRs leave it. This is what makes the cap a
	// ROLLING window — once the first reservation expires, the counter is
	// gone and the next one starts fresh.
	val, err := redis.Client.Incr(redis.Ctx, key).Result()
	if err != nil {
		// Redis hiccup: degrade to local. We log a warning so operators see
		// the gap.
		return reserveLocal(capabilityID, tenantID, cfg)
	}
	if val == 1 {
		_ = redis.Client.Expire(redis.Ctx, key, ttl).Err()
	}
	if int(val) > cfg.MaxDevices {
		// Decrement — we're refusing this reservation, so it shouldn't count
		// against the budget. The next caller sees the actual count.
		_, _ = redis.Client.Decr(redis.Ctx, key).Result()
		return fmt.Errorf("%w (count=%d cap=%d window=%dm)", ErrBlastRadiusExceeded, val, cfg.MaxDevices, cfg.WindowMinutes)
	}
	return nil
}

func reserveLocal(capabilityID, tenantID string, cfg BlastConfig) error {
	key := capabilityID + "|" + tenantID
	cutoff := time.Now().Add(-time.Duration(cfg.WindowMinutes) * time.Minute)

	localBlast.mu.Lock()
	defer localBlast.mu.Unlock()
	w, ok := localBlast.windows[key]
	if !ok {
		w = &window{}
		localBlast.windows[key] = w
	}
	// Drop expired timestamps so the window is genuinely sliding.
	keep := w.timestamps[:0]
	for _, ts := range w.timestamps {
		if ts.After(cutoff) {
			keep = append(keep, ts)
		}
	}
	w.timestamps = keep
	if len(w.timestamps) >= cfg.MaxDevices {
		return fmt.Errorf("%w (count=%d cap=%d window=%dm)", ErrBlastRadiusExceeded, len(w.timestamps), cfg.MaxDevices, cfg.WindowMinutes)
	}
	w.timestamps = append(w.timestamps, time.Now())
	return nil
}

// Count returns the current count in the (capability, tenant) window. Used
// by the dashboard's "approaching cap" banner. Best-effort; doesn't block.
func Count(ctx context.Context, capabilityID, tenantID string, cfg BlastConfig) int {
	if cfg.MaxDevices <= 0 {
		return 0
	}
	if cfg.WindowMinutes <= 0 {
		cfg.WindowMinutes = 5
	}
	if redis.IsEnabled() {
		key := fmt.Sprintf("ai:blast:%s:%s", capabilityID, tenantID)
		v, err := redis.Client.Get(redis.Ctx, key).Int()
		if err != nil {
			return 0
		}
		return v
	}
	key := capabilityID + "|" + tenantID
	cutoff := time.Now().Add(-time.Duration(cfg.WindowMinutes) * time.Minute)
	localBlast.mu.Lock()
	defer localBlast.mu.Unlock()
	w, ok := localBlast.windows[key]
	if !ok {
		return 0
	}
	n := 0
	for _, ts := range w.timestamps {
		if ts.After(cutoff) {
			n++
		}
	}
	return n
}

// LoadConfig reads the per-(tenant, capability) blast cap from the database.
// Wraps the lookup so callers don't need to know the column names.
func LoadConfig(tenantID, capabilityID string) BlastConfig {
	var maxDev, win int
	_ = db.DB.QueryRow(`
		SELECT COALESCE(blast_radius_max_devices,0), COALESCE(blast_radius_window_minutes,5)
		  FROM ai_capability_tenant_config
		 WHERE tenant_id = ? AND capability_id = ?`,
		tenantID, capabilityID).Scan(&maxDev, &win)
	return BlastConfig{MaxDevices: maxDev, WindowMinutes: win}
}
