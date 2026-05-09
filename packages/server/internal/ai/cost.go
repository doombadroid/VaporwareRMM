package ai

import (
	"context"
	"errors"
	"fmt"
	"time"

	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/redis"
)

// ErrCostCap signals the per-tenant per-day cap was reached. The chokepoint
// converts this into a 429-ish response and the dashboard surfaces it.
var ErrCostCap = errors.New("ai: per-tenant per-day cost cap reached")

// CostKind picks which budget the call is debited against. Embeddings and
// chat are intentionally separate caps so a runaway re-index can't drain the
// chat budget (or vice versa).
type CostKind int

const (
	CostChat CostKind = iota
	CostEmbedding
)

// dailyKey is the Redis key for a tenant's daily counter. Keyed on UTC date
// so we don't need a clean-up job — keys age out after the next day's TTL.
func dailyKey(tenantID string, kind CostKind, day time.Time) string {
	suffix := "chat"
	if kind == CostEmbedding {
		suffix = "embed"
	}
	return fmt.Sprintf("ai:cost:%s:%s:%s", tenantID, day.UTC().Format("2006-01-02"), suffix)
}

// loadCap reads the appropriate per-day cap for a tenant in micros. 0 means
// "unlimited" (operator opt-in only).
func loadCap(tenantID string, kind CostKind) (int64, error) {
	var chat, embed int64
	err := db.DB.QueryRow(`
		SELECT COALESCE(ai_max_chat_cost_per_day_micros,0),
		       COALESCE(ai_max_embedding_cost_per_day_micros,0)
		  FROM tenants WHERE id = ?`, tenantID).Scan(&chat, &embed)
	if err != nil {
		return 0, fmt.Errorf("ai: load cost cap for tenant %s: %w", tenantID, err)
	}
	if kind == CostEmbedding {
		return embed, nil
	}
	return chat, nil
}

// ReserveCost performs an atomic check-and-debit against the per-tenant cap.
//
// With Redis available it uses INCRBY then checks the new value vs the cap;
// if exceeded, it decrements back and returns ErrCostCap. Without Redis we
// fall through to a Postgres SELECT FOR UPDATE on the tenants row — slower
// but still correct under concurrency.
//
// We reserve the *estimated* cost up front (token-count × per-1k-rate). The
// post-call layer reconciles against the provider-reported usage and adjusts
// the counter. Over-reservation under load is preferred to under-reservation;
// the reconcile step releases unused budget within seconds.
func ReserveCost(ctx context.Context, tenantID string, kind CostKind, micros int64) error {
	if micros <= 0 {
		return nil
	}
	cap, err := loadCap(tenantID, kind)
	if err != nil {
		return err
	}
	if cap == 0 {
		// Unlimited; still record so the dashboard sees usage.
		return recordOnlyRedis(ctx, tenantID, kind, micros)
	}
	if redis.IsEnabled() {
		key := dailyKey(tenantID, kind, time.Now())
		newVal, err := redis.Client.IncrBy(redis.Ctx, key, micros).Result()
		if err == nil {
			// Set a 36-hour TTL once per key (idempotent).
			_ = redis.Client.Expire(redis.Ctx, key, 36*time.Hour).Err()
			if newVal > cap {
				_, _ = redis.Client.DecrBy(redis.Ctx, key, micros).Result()
				return ErrCostCap
			}
			return nil
		}
		// Redis errored mid-flight; fall through to the DB path.
	}
	return reserveViaDB(ctx, tenantID, kind, micros, cap)
}

// ReleaseCost returns over-reserved budget to the counter. Called by the
// chokepoint after the call completes with an actual cost lower than the
// reserved estimate. Negative actual costs are a programming bug — caller
// passes (reserved - actual) which must be >= 0.
func ReleaseCost(ctx context.Context, tenantID string, kind CostKind, micros int64) {
	if micros <= 0 {
		return
	}
	if redis.IsEnabled() {
		key := dailyKey(tenantID, kind, time.Now())
		_, _ = redis.Client.DecrBy(redis.Ctx, key, micros).Result()
	}
	// DB path doesn't carry a separate counter; the audit log is the source
	// of truth for accounting and the next ReserveCost will recompute.
}

func recordOnlyRedis(ctx context.Context, tenantID string, kind CostKind, micros int64) error {
	if !redis.IsEnabled() {
		return nil
	}
	key := dailyKey(tenantID, kind, time.Now())
	if _, err := redis.Client.IncrBy(redis.Ctx, key, micros).Result(); err != nil {
		return nil
	}
	_ = redis.Client.Expire(redis.Ctx, key, 36*time.Hour).Err()
	return nil
}

// reserveViaDB is the Redis-down path. We sum today's runs from ai_runs
// inside a single transaction with a lock on the tenants row; the comparison
// is atomic across goroutines + processes.
func reserveViaDB(ctx context.Context, tenantID string, kind CostKind, micros, cap int64) error {
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ai: cost reserve begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`SELECT id FROM tenants WHERE id = ? FOR UPDATE`, tenantID); err != nil {
		return fmt.Errorf("ai: cost reserve lock: %w", err)
	}

	var spent int64
	dayStart := time.Now().UTC().Truncate(24 * time.Hour).Unix()
	runType := "chat"
	if kind == CostEmbedding {
		runType = "embed"
	}
	if err := tx.QueryRow(`
		SELECT COALESCE(SUM(cost_usd_micros),0)
		  FROM ai_runs
		 WHERE tenant_id = ? AND created_at >= ? AND run_type = ?`,
		tenantID, dayStart, runType,
	).Scan(&spent); err != nil {
		return fmt.Errorf("ai: cost reserve sum: %w", err)
	}
	if spent+micros > cap {
		return ErrCostCap
	}
	return tx.Commit()
}
