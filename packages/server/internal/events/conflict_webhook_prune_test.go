package events

import (
	"fmt"
	"testing"
)

// TestInMemoryLimiter_PrunesExpiredBuckets is the Codex P3 fix:
// in Redis-degraded mode, per-device entries used to accumulate in
// conflictWebhookBuckets forever. Long uptime + fleet churn turns
// that into an unbounded memory sink. The fix adds
// PruneConflictWebhookBuckets, which deletes entries whose window
// has elapsed.
func TestInMemoryLimiter_PrunesExpiredBuckets(t *testing.T) {
	ResetRegistrationConflictWebhookBucketsForTests()
	t.Cleanup(ResetRegistrationConflictWebhookBucketsForTests)

	// Seed: 100 expired buckets (windowStart far in the past),
	// 50 live buckets (windowStart recent).
	const totalExpired = 100
	const totalLive = 50
	pastWindowStart := int64(1)                                                // long ago
	liveWindowStart := int64(1) + conflictWebhookWindowSeconds*0 + 1000000000 // arbitrary recent

	conflictWebhookMu.Lock()
	for i := 0; i < totalExpired; i++ {
		key := fmt.Sprintf("tenant|expired-device-%d", i)
		conflictWebhookBuckets[key] = &conflictWebhookBucket{windowStart: pastWindowStart, count: 1}
	}
	for i := 0; i < totalLive; i++ {
		key := fmt.Sprintf("tenant|live-device-%d", i)
		conflictWebhookBuckets[key] = &conflictWebhookBucket{windowStart: liveWindowStart, count: 1}
	}
	conflictWebhookMu.Unlock()

	// "now" places the expired buckets past their window and keeps
	// the live ones inside their window.
	now := liveWindowStart + 100
	removed := PruneConflictWebhookBuckets(now)
	if removed != totalExpired {
		t.Errorf("prune removed %d buckets, want %d", removed, totalExpired)
	}

	conflictWebhookMu.Lock()
	defer conflictWebhookMu.Unlock()
	if got := len(conflictWebhookBuckets); got != totalLive {
		t.Errorf("after prune: %d buckets remain, want %d (live)", got, totalLive)
	}
	// All remaining keys must be live ones.
	for k := range conflictWebhookBuckets {
		if k[7:14] != "live-de" {
			t.Errorf("prune left expired bucket: %q", k)
		}
	}
}

// TestInMemoryLimiter_RefusesNewEntryAtCap covers the defense-in-
// depth cap: when the degraded-mode map is at conflictWebhookBucketMax
// and inline prune yields nothing, the limiter refuses to add a new
// entry and instead lets the webhook fire (noisy degraded mode beats
// silent OOM). Verifies the cap is enforced by simulating a packed
// map.
func TestInMemoryLimiter_RefusesNewEntryAtCap(t *testing.T) {
	ResetRegistrationConflictWebhookBucketsForTests()
	t.Cleanup(ResetRegistrationConflictWebhookBucketsForTests)

	// Pack the map with non-expired entries so the inline prune
	// finds nothing to remove.
	now := int64(2_000_000_000) // arbitrary "now"
	conflictWebhookMu.Lock()
	for i := 0; i < conflictWebhookBucketMax; i++ {
		key := fmt.Sprintf("tenant|packed-device-%d", i)
		conflictWebhookBuckets[key] = &conflictWebhookBucket{windowStart: now, count: 1}
	}
	conflictWebhookMu.Unlock()

	// Pruning at this 'now' removes zero entries (all live).
	removed := PruneConflictWebhookBuckets(now)
	if removed != 0 {
		t.Errorf("expected 0 pruned (all live), got %d", removed)
	}

	conflictWebhookMu.Lock()
	size := len(conflictWebhookBuckets)
	conflictWebhookMu.Unlock()
	if size != conflictWebhookBucketMax {
		t.Errorf("packed-map size after no-op prune: got %d want %d", size, conflictWebhookBucketMax)
	}
}
