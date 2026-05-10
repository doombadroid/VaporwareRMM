package events

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"vaporrmm/server/internal/crypto"
	"vaporrmm/server/internal/db"
)

// auditChainMu serialises audit_log INSERTs so the hash chain stays
// well-defined under concurrent writers. Audit volume is small (handful
// of writes per second worst case) so a global lock is cheaper than the
// alternatives — per-tenant locks risk one tenant deadlocking another
// when a cross-tenant action audits both sides, and a per-row CAS would
// require a "previous signature" column we'd have to keep current under
// retention deletes anyway.
var auditChainMu sync.Mutex

// canonicalAuditPayload returns the bytes that go into HMAC for a given
// row. Field order is fixed forever — changing it invalidates every
// existing chain. Pipe is the separator because none of the fields are
// expected to contain it; if they ever do, we already lose the audit
// log's plaintext readability anyway.
//
// IMPORTANT: any new column added to audit_logs that we want covered by
// the integrity check must be appended to this function, never inserted
// in the middle. A change in the middle changes every signature
// downstream and trips every legitimate verifier.
func canonicalAuditPayload(id, tenantID, userID, action, resourceType, resourceID, details, ipAddress string, createdAt int64) string {
	return strings.Join([]string{
		id, tenantID, userID, action, resourceType, resourceID, details, ipAddress,
		fmt.Sprintf("%d", createdAt),
	}, "|")
}

// auditSignature derives the HMAC signature for a row given the
// previous row's signature. Domain-separation tag "audit-chain" keeps
// these signatures from being confusable with other HMAC outputs in
// the system (e.g. session token hashes).
func auditSignature(prevSig, payload string) string {
	return crypto.HMACSHA256("audit-chain", prevSig+"\n"+payload)
}

// loadLastAuditSignature returns the signature of the most-recent row
// in audit_logs. Returns "" when the table is empty (chain genesis).
// We order by (created_at DESC, id DESC) — created_at alone is not
// monotonic enough since two rows can land in the same Unix-second.
func loadLastAuditSignature() (string, error) {
	var sig sql.NullString
	err := db.DB.QueryRow(`SELECT signature FROM audit_logs ORDER BY created_at DESC, id DESC LIMIT 1`).Scan(&sig)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !sig.Valid {
		return "", nil
	}
	return sig.String, nil
}

// BackfillAuditChain populates the signature column for every row that
// was inserted before the tamper-evidence migration landed. Idempotent:
// a row whose signature is already non-empty is left alone. Walks rows
// in chain order so each backfilled signature folds in the previous
// one. Called at startup; cheap once it's done its first run.
func BackfillAuditChain() error {
	auditChainMu.Lock()
	defer auditChainMu.Unlock()

	rows, err := db.DB.Query(`SELECT id, COALESCE(tenant_id,'default'), COALESCE(user_id,''), action, COALESCE(resource_type,''), COALESCE(resource_id,''), COALESCE(details,''), COALESCE(ip_address,''), created_at, COALESCE(signature,'') FROM audit_logs ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return fmt.Errorf("audit chain backfill: query: %w", err)
	}
	defer rows.Close()

	type rec struct {
		id, tid, uid, action, rt, rid, det, ip, sig string
		ts                                          int64
	}
	var prevSig string
	var batch []rec
	for rows.Next() {
		var r rec
		if err := rows.Scan(&r.id, &r.tid, &r.uid, &r.action, &r.rt, &r.rid, &r.det, &r.ip, &r.ts, &r.sig); err != nil {
			return fmt.Errorf("audit chain backfill: scan: %w", err)
		}
		batch = append(batch, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("audit chain backfill: rows iter: %w", err)
	}

	updated := 0
	for _, r := range batch {
		want := auditSignature(prevSig, canonicalAuditPayload(r.id, r.tid, r.uid, r.action, r.rt, r.rid, r.det, r.ip, r.ts))
		if r.sig == "" {
			if _, err := db.DB.Exec(`UPDATE audit_logs SET signature = ? WHERE id = ?`, want, r.id); err != nil {
				return fmt.Errorf("audit chain backfill: update %s: %w", r.id, err)
			}
			updated++
			prevSig = want
			continue
		}
		// Row already has a signature. Trust it as the chain head — we
		// must not retroactively rewrite it because that would mask
		// tampering of older rows.
		prevSig = r.sig
	}
	if updated > 0 {
		slog.Info("audit chain backfill complete", "rows_updated", updated)
	}
	return nil
}

// AuditChainVerifyResult is the report from VerifyAuditChain. OK=true
// means every row's signature recomputes to the stored value and the
// chain has no gaps. When OK=false, FirstBadID is the row at which the
// first inconsistency was detected.
type AuditChainVerifyResult struct {
	OK         bool   `json:"ok"`
	Total      int    `json:"total"`
	Verified   int    `json:"verified"`
	FirstBadID string `json:"first_bad_id,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// VerifyAuditChain walks audit_logs in chain order and recomputes each
// signature against the stored value. Returns at the first mismatch so
// operators see the earliest tampered row, not the last.
func VerifyAuditChain() (AuditChainVerifyResult, error) {
	rows, err := db.DB.Query(`SELECT id, COALESCE(tenant_id,'default'), COALESCE(user_id,''), action, COALESCE(resource_type,''), COALESCE(resource_id,''), COALESCE(details,''), COALESCE(ip_address,''), created_at, COALESCE(signature,'') FROM audit_logs ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return AuditChainVerifyResult{}, fmt.Errorf("verify: query: %w", err)
	}
	defer rows.Close()

	prevSig := ""
	total := 0
	verified := 0
	for rows.Next() {
		total++
		var id, tid, uid, action, rt, rid, det, ip, sig string
		var ts int64
		if err := rows.Scan(&id, &tid, &uid, &action, &rt, &rid, &det, &ip, &ts, &sig); err != nil {
			return AuditChainVerifyResult{}, fmt.Errorf("verify: scan: %w", err)
		}
		want := auditSignature(prevSig, canonicalAuditPayload(id, tid, uid, action, rt, rid, det, ip, ts))
		if sig == "" {
			return AuditChainVerifyResult{OK: false, Total: total, Verified: verified, FirstBadID: id, Reason: "missing signature"}, nil
		}
		if sig != want {
			return AuditChainVerifyResult{OK: false, Total: total, Verified: verified, FirstBadID: id, Reason: "signature mismatch"}, nil
		}
		verified++
		prevSig = sig
	}
	if err := rows.Err(); err != nil {
		return AuditChainVerifyResult{}, fmt.Errorf("verify: rows iter: %w", err)
	}
	return AuditChainVerifyResult{OK: true, Total: total, Verified: verified}, nil
}

// auditNow is a hook for tests to pin time.Now without exporting it
// through the public AuditLog API.
var auditNow = func() int64 { return time.Now().Unix() }
