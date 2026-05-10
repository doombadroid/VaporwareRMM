package events

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"vaporrmm/server/internal/crypto"
	"vaporrmm/server/internal/db"

	"github.com/google/uuid"
)

// AuditCompactionAction is the sentinel value stored in audit_logs.action
// for a compaction marker. Compaction records bridge a deleted range —
// the verifier recognises them and continues the chain using the
// stored end_sig instead of the compaction record's own signature.
const AuditCompactionAction = "audit.compaction"

// auditChainMu serialises every audit_log INSERT and the
// per-tenant chain_seq assignment underneath it. Audit volume is low
// (handful of writes per second worst case) so a single global lock is
// cheaper than the alternatives: per-tenant locks risk one tenant
// deadlocking another when a cross-tenant action audits both sides;
// a per-row CAS would still need a global allocator for chain_seq.
var auditChainMu sync.Mutex

// canonicalAuditPayload returns the bytes that go into HMAC for a row.
// Field order is fixed forever — changing it invalidates every
// existing chain. chain_seq is intentionally NOT included here: the
// signature must remain stable across a backfill that assigns it, and
// the verifier reads rows in chain_seq order anyway so the sequence
// number is structural metadata, not a covered field.
func canonicalAuditPayload(id, tenantID, userID, action, resourceType, resourceID, details, ipAddress string, createdAt int64) string {
	return strings.Join([]string{
		id, tenantID, userID, action, resourceType, resourceID, details, ipAddress,
		fmt.Sprintf("%d", createdAt),
	}, "|")
}

func auditSignature(prevSig, payload string) string {
	return crypto.HMACSHA256("audit-chain", prevSig+"\n"+payload)
}

// loadLastAuditState returns the chain head for a given tenant: the
// signature a new row should chain *from*, plus the highest chain_seq
// currently in use. Returns ("", 0, nil) when the tenant's chain is
// empty.
//
// IMPORTANT for compaction: if the most recent row is a compaction
// record, the "previous signature" for the next write is the end_sig
// stored in the CR's details (the sig of the last deleted row), NOT
// the CR's own signature. The verifier follows the same rule, so
// writer and verifier agree on the chain transition.
func loadLastAuditState(tenantID string) (sig string, seq int64, err error) {
	var sigN sql.NullString
	var seqN sql.NullInt64
	var actionN sql.NullString
	var detailsN sql.NullString
	err = db.DB.QueryRow(`SELECT signature, chain_seq, action, details FROM audit_logs WHERE tenant_id = ? ORDER BY chain_seq DESC LIMIT 1`, tenantID).Scan(&sigN, &seqN, &actionN, &detailsN)
	if err == sql.ErrNoRows {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	if seqN.Valid {
		seq = seqN.Int64
	}
	if actionN.Valid && actionN.String == AuditCompactionAction && detailsN.Valid {
		if end := compactionEndSig(detailsN.String); end != "" {
			return end, seq, nil
		}
	}
	if sigN.Valid {
		sig = sigN.String
	}
	return sig, seq, nil
}

// BackfillAuditChain populates signature and chain_seq for every row
// inserted before migration 043 landed. Idempotent: rows whose
// signature is already non-empty AND whose chain_seq is non-zero are
// trusted as-is. A row with signature set but chain_seq=0 (i.e. the
// migration-042 chain) gets a chain_seq assigned without rewriting the
// signature; rewriting would mask tampering of older rows.
//
// Walks rows in (tenant_id, created_at, id) order and assigns chain_seq
// starting from 1 per tenant. New writes after this function returns
// pick up at MAX(chain_seq) + 1.
func BackfillAuditChain() error {
	auditChainMu.Lock()
	defer auditChainMu.Unlock()

	rows, err := db.DB.Query(`SELECT id, COALESCE(tenant_id,'default'), COALESCE(user_id,''), action, COALESCE(resource_type,''), COALESCE(resource_id,''), COALESCE(details,''), COALESCE(ip_address,''), created_at, COALESCE(signature,''), COALESCE(chain_seq,0) FROM audit_logs ORDER BY tenant_id ASC, created_at ASC, id ASC`)
	if err != nil {
		return fmt.Errorf("audit chain backfill: query: %w", err)
	}
	defer rows.Close()

	type rec struct {
		id, tid, uid, action, rt, rid, det, ip, sig string
		ts, seq                                     int64
	}
	var batch []rec
	for rows.Next() {
		var r rec
		if err := rows.Scan(&r.id, &r.tid, &r.uid, &r.action, &r.rt, &r.rid, &r.det, &r.ip, &r.ts, &r.sig, &r.seq); err != nil {
			return fmt.Errorf("audit chain backfill: scan: %w", err)
		}
		batch = append(batch, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("audit chain backfill: rows iter: %w", err)
	}

	// Per-tenant chain heads as we walk.
	prevByTenant := map[string]string{}
	seqByTenant := map[string]int64{}
	updated := 0
	for _, r := range batch {
		prev := prevByTenant[r.tid]
		seq := seqByTenant[r.tid] + 1

		wantSig := auditSignature(prev, canonicalAuditPayload(r.id, r.tid, r.uid, r.action, r.rt, r.rid, r.det, r.ip, r.ts))

		needSig := r.sig == ""
		needSeq := r.seq == 0
		if needSig || needSeq {
			sig := r.sig
			if needSig {
				sig = wantSig
			}
			if _, err := db.DB.Exec(`UPDATE audit_logs SET signature = ?, chain_seq = ? WHERE id = ?`, sig, seq, r.id); err != nil {
				return fmt.Errorf("audit chain backfill: update %s: %w", r.id, err)
			}
			updated++
			prevByTenant[r.tid] = sig
		} else {
			// Row already signed AND sequenced before — trust the
			// stored signature, don't rewrite it.
			prevByTenant[r.tid] = r.sig
		}
		// Compaction rows bridge a deleted range; the chain head
		// after this row is the row's stored end_sig, NOT the row's
		// own signature. Read it from the details JSON.
		if r.action == AuditCompactionAction {
			if end := compactionEndSig(r.det); end != "" {
				prevByTenant[r.tid] = end
			}
		}
		seqByTenant[r.tid] = seq
	}
	if updated > 0 {
		slog.Info("audit chain backfill complete", "rows_updated", updated)
	}
	return nil
}

// compactionDetails is the JSON payload stored in a compaction record's
// details column. end_sig is the signature of the LAST deleted row —
// downstream rows still chain from it.
type compactionDetails struct {
	FirstDeletedID string `json:"first_deleted_id"`
	LastDeletedID  string `json:"last_deleted_id"`
	Count          int    `json:"count"`
	EndSig         string `json:"end_sig"`
	CutoffUnix     int64  `json:"cutoff_unix"`
}

func compactionEndSig(detailsJSON string) string {
	var d compactionDetails
	if err := json.Unmarshal([]byte(detailsJSON), &d); err != nil {
		return ""
	}
	return d.EndSig
}

// AuditChainVerifyResult is the report from VerifyAuditChain for one
// tenant. OK=true means every row's signature recomputes and every
// compaction record bridges the gap it claims.
type AuditChainVerifyResult struct {
	TenantID   string `json:"tenant_id"`
	OK         bool   `json:"ok"`
	Total      int    `json:"total"`
	Verified   int    `json:"verified"`
	FirstBadID string `json:"first_bad_id,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// VerifyAuditChain walks audit_logs in per-tenant chain_seq order. If
// tenantID is empty the verifier runs for every tenant and returns one
// result per tenant. If tenantID is non-empty only that tenant's chain
// is checked.
//
// Compaction records (action == AuditCompactionAction) are recognised:
// the verifier validates the compaction record's own signature against
// the current prev_sig, then advances prev_sig to the end_sig stored
// in details so the next live row chains correctly.
func VerifyAuditChain(tenantID string) ([]AuditChainVerifyResult, error) {
	tenants := []string{tenantID}
	if tenantID == "" {
		t, err := listTenantsWithAuditRows()
		if err != nil {
			return nil, err
		}
		tenants = t
	}
	out := make([]AuditChainVerifyResult, 0, len(tenants))
	for _, t := range tenants {
		res, err := verifyOneTenant(t)
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, nil
}

func listTenantsWithAuditRows() ([]string, error) {
	rows, err := db.DB.Query(`SELECT DISTINCT tenant_id FROM audit_logs ORDER BY tenant_id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func verifyOneTenant(tenantID string) (AuditChainVerifyResult, error) {
	rows, err := db.DB.Query(`SELECT id, COALESCE(user_id,''), action, COALESCE(resource_type,''), COALESCE(resource_id,''), COALESCE(details,''), COALESCE(ip_address,''), created_at, COALESCE(signature,''), COALESCE(chain_seq,0) FROM audit_logs WHERE tenant_id = ? ORDER BY chain_seq ASC`, tenantID)
	if err != nil {
		return AuditChainVerifyResult{TenantID: tenantID}, fmt.Errorf("verify: query: %w", err)
	}
	defer rows.Close()

	res := AuditChainVerifyResult{TenantID: tenantID, OK: true}
	prev := ""
	for rows.Next() {
		res.Total++
		var id, uid, action, rt, rid, det, ip, sig string
		var ts, seq int64
		if err := rows.Scan(&id, &uid, &action, &rt, &rid, &det, &ip, &ts, &sig, &seq); err != nil {
			return res, fmt.Errorf("verify: scan: %w", err)
		}
		expected := auditSignature(prev, canonicalAuditPayload(id, tenantID, uid, action, rt, rid, det, ip, ts))
		if sig == "" {
			res.OK = false
			res.FirstBadID = id
			res.Reason = "missing signature"
			return res, nil
		}
		if sig != expected {
			res.OK = false
			res.FirstBadID = id
			res.Reason = "signature mismatch"
			return res, nil
		}
		res.Verified++
		if action == AuditCompactionAction {
			end := compactionEndSig(det)
			if end == "" {
				res.OK = false
				res.FirstBadID = id
				res.Reason = "compaction record missing end_sig"
				return res, nil
			}
			prev = end
		} else {
			prev = sig
		}
	}
	return res, rows.Err()
}

// CompactAuditChainForTenant deletes every audit_log row for tenantID
// whose created_at is strictly less than cutoffUnix, AFTER inserting a
// compaction record that bridges the deletion. Returns the number of
// rows removed and the compaction record's id.
//
// Holds auditChainMu for the duration so a concurrent write can't
// land in the middle of the deleted range. Wrapping the SELECT +
// INSERT + DELETE in a transaction would be tighter but our Wrapper
// doesn't expose Begin cleanly and the mutex is the actual ordering
// guarantee anyway.
//
// Idempotent in the sense that calling it twice with the same cutoff
// after the first call has nothing to delete simply returns (0, "",
// nil). A second call with a later cutoff will produce a second
// compaction record bridging the new gap.
func CompactAuditChainForTenant(tenantID string, cutoffUnix int64) (int, string, error) {
	if tenantID == "" {
		return 0, "", fmt.Errorf("compact: tenant_id required")
	}
	auditChainMu.Lock()
	defer auditChainMu.Unlock()

	// 1. Identify the rows to delete in chain order. We must capture
	//    their min chain_seq (CR claims that slot) and the signature
	//    of the highest-seq deleted row (end_sig for the bridge).
	rows, err := db.DB.Query(`SELECT id, chain_seq, signature FROM audit_logs WHERE tenant_id = ? AND created_at < ? ORDER BY chain_seq ASC`, tenantID, cutoffUnix)
	if err != nil {
		return 0, "", fmt.Errorf("compact: list: %w", err)
	}
	type tomb struct {
		id  string
		seq int64
		sig string
	}
	var toDelete []tomb
	for rows.Next() {
		var t tomb
		if err := rows.Scan(&t.id, &t.seq, &t.sig); err != nil {
			rows.Close()
			return 0, "", fmt.Errorf("compact: scan: %w", err)
		}
		toDelete = append(toDelete, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, "", fmt.Errorf("compact: iter: %w", err)
	}
	if len(toDelete) == 0 {
		return 0, "", nil
	}

	first := toDelete[0]
	last := toDelete[len(toDelete)-1]
	if first.sig == "" || last.sig == "" {
		return 0, "", fmt.Errorf("compact: deleted range has unsigned rows — refusing to bridge")
	}

	// 2. Build the compaction record. CR claims the smallest deleted
	//    chain_seq so it appears in the chain where the deleted range
	//    used to be. Its signature is HMAC over (prev_sig || canonical)
	//    where prev_sig is the signature of the row immediately before
	//    first.seq — i.e. the last surviving older row, or "" if the
	//    deletion covers the chain head.
	var prevSig string
	err = db.DB.QueryRow(`SELECT signature FROM audit_logs WHERE tenant_id = ? AND chain_seq < ? ORDER BY chain_seq DESC LIMIT 1`, tenantID, first.seq).Scan(&prevSig)
	if err == sql.ErrNoRows {
		prevSig = "" // first.seq is at or near the chain head
	} else if err != nil {
		return 0, "", fmt.Errorf("compact: read predecessor sig: %w", err)
	}

	det := compactionDetails{
		FirstDeletedID: first.id,
		LastDeletedID:  last.id,
		Count:          len(toDelete),
		EndSig:         last.sig,
		CutoffUnix:     cutoffUnix,
	}
	detJSON, _ := json.Marshal(det)

	crID := newAuditID()
	crTS := auditNow()
	crSig := auditSignature(prevSig, canonicalAuditPayload(crID, tenantID, "system", AuditCompactionAction, "audit_log", "", string(detJSON), "", crTS))

	// 3. Insert CR with chain_seq == first.seq. We have to delete the
	//    range BEFORE inserting CR (chain_seq must be unique per
	//    tenant in any future migration, and even without a constraint
	//    we want the row count to be right). Order: delete first, then
	//    insert.
	for _, t := range toDelete {
		if _, err := db.DB.Exec(`DELETE FROM audit_logs WHERE id = ?`, t.id); err != nil {
			return 0, "", fmt.Errorf("compact: delete %s: %w", t.id, err)
		}
	}
	if _, err := db.DB.Exec(
		`INSERT INTO audit_logs (id, user_id, action, resource_type, resource_id, details, ip_address, created_at, tenant_id, signature, chain_seq) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		crID, "system", AuditCompactionAction, "audit_log", "", string(detJSON), "", crTS, tenantID, crSig, first.seq,
	); err != nil {
		return 0, "", fmt.Errorf("compact: insert CR: %w", err)
	}
	slog.Info("audit chain compaction", "tenant", tenantID, "deleted", len(toDelete), "first_id", first.id, "last_id", last.id, "cr_id", crID)
	return len(toDelete), crID, nil
}

// newAuditID returns a UUIDv7 string for audit rows. v7 is time-ordered
// so within a tenant the chain_seq ordering is consistent even when
// the canonical (created_at, id) lookup is used as a fallback.
var newAuditID = func() string {
	if v, err := uuid.NewV7(); err == nil {
		return v.String()
	}
	return uuid.New().String()
}

// auditNow is a hook for tests to pin time.Now without exporting it.
var auditNow = func() int64 { return time.Now().Unix() }
