package events

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"vaporrmm/server/internal/crypto"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/email"
	"vaporrmm/server/internal/httputil"
	"vaporrmm/server/internal/redis"

	"github.com/gofiber/websocket/v2"
)

// wsClientSendBuf is the per-connection outbound queue depth. A slow
// reader fills the buffer, after which we drop messages with a log
// rather than block every other client. 64 is large enough that a
// burst of dashboard activity (a fleet-wide patch run, a bulk command)
// doesn't drop on a healthy client; small enough that one stuck
// connection only loses ~1MB of messages before we cut its losses.
const wsClientSendBuf = 64

// WSClientInfo holds metadata for a connected WebSocket client plus a
// bounded outbound queue. Each client has a goroutine (started by the
// /ws upgrade handler) that drains Send and writes to the conn. The
// broadcast path pushes to Send non-blocking; if the buffer is full,
// the message is dropped with a log so one slow reader can't stall
// every other dashboard.
type WSClientInfo struct {
	UserID   string
	TenantID string
	Role     string
	Send     chan []byte
}

var (
	WSClients = make(map[*websocket.Conn]*WSClientInfo)
	WSMu      sync.RWMutex
)

// wsEnvelope wraps a broadcast so subscribers on other nodes can re-apply
// the filter against THEIR local connections. We can't filter at publish time
// because the publisher doesn't know which clients are connected on other nodes.
//
// "all" used to be a valid Kind for unfiltered cross-tenant broadcast. It was
// removed because it made cross-tenant leaks one missing argument away — a
// future "system notification" feature should be a new function with tenant
// scoping built in, not a revival of WSBroadcastMessage.
type wsEnvelope struct {
	Kind     string          `json:"k"`           // "filtered"
	TenantID string          `json:"t,omitempty"`
	OwnerID  string          `json:"o,omitempty"`
	Payload  json.RawMessage `json:"p"`
}

// WSBroadcastFiltered sends msg to:
//   - any super_admin (cross-tenant)
//   - admins of the same tenant
//   - the device owner (matching userID)
//
// Skips clients in other tenants. Redis-backed when enabled (publish only;
// the subscriber fans out locally on every node) so a multi-node deployment
// stays consistent. Local-only fallback when Redis is off.
func WSBroadcastFiltered(tenantID, ownerID string, msg map[string]interface{}) {
	payload, err := json.Marshal(msg)
	if err != nil {
		return
	}
	if redis.IsEnabled() {
		env, err := json.Marshal(wsEnvelope{Kind: "filtered", TenantID: tenantID, OwnerID: ownerID, Payload: payload})
		if err == nil {
			if err := redis.PublishWSMessage(env); err == nil {
				return
			} else {
				slog.Warn("redis publish failed, falling back to local-only filtered broadcast", "error", err)
			}
		}
	}
	wsFilteredLocal(tenantID, ownerID, payload)
}

func wsFilteredLocal(tenantID, ownerID string, data []byte) {
	WSMu.RLock()
	defer WSMu.RUnlock()
	for _, info := range WSClients {
		if info.Role == "super_admin" {
			pushOrDrop(info, data)
			continue
		}
		if info.TenantID != tenantID {
			continue
		}
		if info.Role == "admin" || (ownerID != "" && info.UserID == ownerID) {
			pushOrDrop(info, data)
		}
	}
}

// pushOrDrop tries to enqueue a message on the client's bounded send
// channel. If the channel is full (slow reader) the message is dropped
// with a log; we explicitly do NOT block the broadcast loop on one
// client. The channel == nil branch covers the legacy code path during
// the rollout window where a connection might exist without a Send
// chan — once every caller is converted that branch can go.
func pushOrDrop(info *WSClientInfo, data []byte) {
	if info == nil || info.Send == nil {
		return
	}
	select {
	case info.Send <- data:
	default:
		slog.Warn("ws backpressure: dropping message for slow reader", "user", info.UserID, "tenant", info.TenantID, "buffer", cap(info.Send))
	}
}

// StartWSRedisSubscriber starts a background goroutine that subscribes to Redis WS
// broadcasts and forwards them to local WebSocket clients. Only "filtered"
// envelopes are honoured; anything that doesn't decode as a tenant-scoped
// envelope is dropped on the floor with a warning. The previous behaviour
// (fall back to unfiltered cross-tenant broadcast) was a tenant-isolation
// footgun and a forward-compat shim with no live publishers anyway.
func StartWSRedisSubscriber() {
	if !redis.IsEnabled() {
		return
	}
	go func() {
		slog.Info("starting redis websocket broadcast subscriber")
		redis.SubscribeWSBroadcast(func(data []byte) {
			var env wsEnvelope
			if err := json.Unmarshal(data, &env); err != nil || env.Kind != "filtered" {
				slog.Warn("ws redis subscriber: dropping unrecognised envelope (only kind='filtered' is honoured)", "err", err, "kind", env.Kind)
				return
			}
			wsFilteredLocal(env.TenantID, env.OwnerID, env.Payload)
		})
	}()
}

// AuditLog records an admin action.
// Use AuditLogTenant when tenant_id is known; AuditLog defaults to 'default'.
func AuditLog(userID, action, resourceType, resourceID, details, ipAddress string) {
	AuditLogTenant("default", userID, action, resourceType, resourceID, details, ipAddress)
}

// AuditLogTenant records an admin action scoped to a tenant. Fires the
// chained insert from a background goroutine so handler latency
// doesn't depend on the chain lock; the goroutine itself is
// synchronous-on-the-mutex (auditChainMu) so concurrent callers see a
// well-defined chain.
func AuditLogTenant(tenantID, userID, action, resourceType, resourceID, details, ipAddress string) {
	go AuditLogTenantSync(tenantID, userID, action, resourceType, resourceID, details, ipAddress)
}

// AuditLogTenantSync is the synchronous variant. Tests use it directly
// so they can rely on the row being committed when the call returns.
// Production handlers should keep using AuditLogTenant; calling this
// from a request handler will serialise that handler with every other
// audit write in flight.
func AuditLogTenantSync(tenantID, userID, action, resourceType, resourceID, details, ipAddress string) {
	if tenantID == "" {
		tenantID = "default"
	}
	auditChainMu.Lock()
	defer auditChainMu.Unlock()

	prevSig, prevSeq, err := loadLastAuditState(tenantID)
	if err != nil {
		slog.Warn("audit log: failed to load chain head; row will write but chain may be discontinuous", "error", err)
	}

	id := newAuditID()
	ts := auditNow()
	sig := auditSignature(prevSig, canonicalAuditPayload(id, tenantID, userID, action, resourceType, resourceID, details, ipAddress, ts))
	seq := prevSeq + 1

	if _, err := db.DB.Exec(
		`INSERT INTO audit_logs (id, user_id, action, resource_type, resource_id, details, ip_address, created_at, tenant_id, signature, chain_seq)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, userID, action, resourceType, resourceID, details, ipAddress, ts, tenantID, sig, seq,
	); err != nil {
		// A write failure on a privileged action is the kind of silent
		// gap the audit log exists to prevent. Log loudly — observ-
		// ability is the only mitigation we have here short of
		// refusing the originating action, which is too far downstream
		// to abort cleanly.
		slog.Error("audit log write failed", "error", err, "action", action, "user", userID, "tenant", tenantID)
	}
}

// conflictWebhookBucket tracks per-(tenant, device) conflict bursts.
// windowStart is the Unix timestamp of the first fire in the current
// 1-hour window; count is the number of conflicts (fired AND
// suppressed) inside that window.
type conflictWebhookBucket struct {
	windowStart int64
	count       int
}

const conflictWebhookWindowSeconds int64 = 3600

var (
	conflictWebhookMu      sync.Mutex
	conflictWebhookBuckets = make(map[string]*conflictWebhookBucket)
)

// TriggerRegistrationConflictWebhook fires the
// device.registration_conflict webhook for the given device, subject
// to the Codex #6 spec's per-device rate limit (1 webhook per device
// per hour). Every call increments the bucket count regardless of
// whether the webhook actually fires, so the next webhook to fire
// can report attempt_count_within_window accurately.
//
// Audit logs are NOT written here — the registration handler logs
// every conflict unconditionally. This function only governs whether
// the external webhook delivery fires.
func TriggerRegistrationConflictWebhook(tenantID, deviceID, claimedHostname, claimedMAC, sourceIP string) {
	if tenantID == "" {
		tenantID = "default"
	}
	now := time.Now().Unix()
	key := tenantID + "|" + deviceID

	conflictWebhookMu.Lock()
	bucket, ok := conflictWebhookBuckets[key]
	if !ok {
		bucket = &conflictWebhookBucket{}
		conflictWebhookBuckets[key] = bucket
	}
	fire := false
	if bucket.windowStart == 0 || now-bucket.windowStart >= conflictWebhookWindowSeconds {
		bucket.windowStart = now
		bucket.count = 1
		fire = true
	} else {
		bucket.count++
	}
	fireCount := bucket.count
	conflictWebhookMu.Unlock()

	if !fire {
		return
	}
	TriggerWebhooks(tenantID, "device.registration_conflict", map[string]interface{}{
		"device_id":                   deviceID,
		"tenant_id":                   tenantID,
		"attempt_count_within_window": fireCount,
		"claimed_hostname":            claimedHostname,
		"claimed_mac":                 claimedMAC,
		"source_ip":                   sourceIP,
		"window_seconds":              conflictWebhookWindowSeconds,
		"timestamp":                   now,
	})
}

// ResetRegistrationConflictWebhookBucketsForTests clears the
// per-device webhook rate-limit state so tests can run in isolation.
// Production code never calls this.
func ResetRegistrationConflictWebhookBucketsForTests() {
	conflictWebhookMu.Lock()
	conflictWebhookBuckets = make(map[string]*conflictWebhookBucket)
	conflictWebhookMu.Unlock()
}

// AdvanceConflictWebhookWindowStartForTests rewinds a bucket's
// windowStart to simulate the passage of time. Production code never
// calls this.
func AdvanceConflictWebhookWindowStartForTests(tenantID, deviceID string, deltaSeconds int64) {
	if tenantID == "" {
		tenantID = "default"
	}
	key := tenantID + "|" + deviceID
	conflictWebhookMu.Lock()
	if bucket, ok := conflictWebhookBuckets[key]; ok {
		bucket.windowStart -= deltaSeconds
	}
	conflictWebhookMu.Unlock()
}

// Webhook outbound is gated by the SSRF helpers in httputil. Tests
// that need to deliver to a 127.0.0.1 httptest server swap these
// out for permissive variants; production never touches them. The
// pointers are concurrency-safe to swap before the test sends any
// request because no concurrent webhook is in flight at that point.
var (
	webhookHostValidator = httputil.RejectPrivateHost
	webhookOutboundClient = httputil.SafeOutboundClient
)

// SetWebhookOutboundForTests swaps both SSRF guards with permissive
// variants so a 127.0.0.1 httptest destination is reachable. Pass
// nil to restore the production defaults.
func SetWebhookOutboundForTests(validator func(string) error, client func(time.Duration) *http.Client) {
	if validator == nil {
		webhookHostValidator = httputil.RejectPrivateHost
	} else {
		webhookHostValidator = validator
	}
	if client == nil {
		webhookOutboundClient = httputil.SafeOutboundClient
	} else {
		webhookOutboundClient = client
	}
}

// TriggerWebhooks fires webhooks subscribed in the given tenant for the named event.
// Pass tenantID="" to fan out across all tenants (system events).
func TriggerWebhooks(tenantID, event string, payload map[string]interface{}) {
	go func() {
		var rows *sql.Rows
		var err error
		if tenantID == "" {
			rows, err = db.DB.Query(`SELECT id, url, secret, events FROM webhooks WHERE enabled = 1`)
		} else {
			rows, err = db.DB.Query(`SELECT id, url, secret, events FROM webhooks WHERE enabled = 1 AND tenant_id = ?`, tenantID)
		}
		if err != nil {
			return
		}
		defer rows.Close()

		for rows.Next() {
			var id, urlStr, secret, events string
			if err := rows.Scan(&id, &urlStr, &secret, &events); err != nil {
				slog.Warn("rows scan failed", "error", err)
			}
			if !strings.Contains(events, event) && events != "*" {
				continue
			}

			plainSecret, err := crypto.Decrypt(secret)
			if err != nil {
				slog.Warn("failed to decrypt webhook secret", "webhook_id", id, "error", err)
				plainSecret = secret
			}
			// Re-validate the destination at fetch time. The URL was checked
			// when the webhook row was created, but DNS rebinding lets a
			// public hostname resolve to a private IP minutes later. The
			// SafeOutboundClient also blocks redirect-based bypasses.
			if err := webhookHostValidator(urlStr); err != nil {
				slog.Warn("webhook destination blocked by SSRF guard", "webhook_id", id, "error", err)
				continue
			}
			body, _ := json.Marshal(payload)
			req, _ := http.NewRequest("POST", urlStr, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-VaporRMM-Event", event)
			if plainSecret != "" {
				sig := hmac.New(sha256.New, []byte(plainSecret))
				sig.Write(body)
				req.Header.Set("X-VaporRMM-Signature", hex.EncodeToString(sig.Sum(nil)))
			}

			resp, err := webhookOutboundClient(10 * time.Second).Do(req)
			if err != nil {
				slog.Warn("webhook delivery failed", "webhook_id", id, "error", err)
				continue
			}
			resp.Body.Close()
		}
		if err := rows.Err(); err != nil {
			slog.Warn("rows iteration error", "error", err)
		}
	}()

	go TriggerEmailAlerts(tenantID, event, payload)
}

// TriggerEmailAlerts loads SMTP settings and alert rules for the given tenant
// and emails recipients matching event_type.
func TriggerEmailAlerts(tenantID, event string, payload map[string]interface{}) {
	if tenantID == "" {
		tenantID = "default"
	}
	var enabled int
	var smtpHost, smtpUser, smtpPassword, smtpFrom string
	var smtpPort int
	err := db.DB.QueryRow(
		`SELECT enabled, smtp_host, smtp_port, smtp_user, smtp_password, smtp_from FROM alert_settings WHERE tenant_id = ?`, tenantID,
	).Scan(&enabled, &smtpHost, &smtpPort, &smtpUser, &smtpPassword, &smtpFrom)
	if err != nil || enabled == 0 || smtpHost == "" || smtpFrom == "" {
		return
	}
	smtpPassword, err = crypto.Decrypt(smtpPassword)
	if err != nil {
		slog.Warn("failed to decrypt smtp password for alerts", "error", err)
	}

	rows, err := db.DB.Query(`SELECT email_recipients FROM alert_rules WHERE event_type = ? AND enabled = 1 AND tenant_id = ?`, event, tenantID)
	if err != nil {
		return
	}
	defer rows.Close()

	var recipients []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			slog.Warn("rows scan failed", "error", err)
		}
		if r != "" {
			for _, raw := range strings.Split(r, ",") {
				// Strip CRLF defensively — anything in alert_rules.email_recipients
				// is operator-supplied via the API; treat it as untrusted at send time.
				clean := strings.TrimSpace(strings.NewReplacer("\r", "", "\n", "").Replace(raw))
				if clean != "" {
					recipients = append(recipients, clean)
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		slog.Warn("rows iteration error", "error", err)
	}

	if len(recipients) == 0 {
		return
	}

	// Same sanitization on subject + From to close the header-injection gap.
	scrub := strings.NewReplacer("\r", "", "\n", "")
	smtpFrom = scrub.Replace(smtpFrom)
	subject := scrub.Replace(fmt.Sprintf("[vaporRMM] Alert: %s", event))
	body := fmt.Sprintf("Event: %s\n\nPayload:\n%s\n\n--\nvaporRMM Alert System",
		event,
		func() string {
			b, _ := json.MarshalIndent(payload, "", "  ")
			return string(b)
		}(),
	)

	msg := []byte("To: " + strings.Join(recipients, ", ") + "\r\n" +
		"From: " + smtpFrom + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"MIME-Version: 1.0\r\n" +
		"\r\n" +
		body + "\r\n")

	var auth smtp.Auth
	if smtpUser != "" && smtpPassword != "" {
		auth = smtp.PlainAuth("", smtpUser, smtpPassword, smtpHost)
	}

	addr := fmt.Sprintf("%s:%d", smtpHost, smtpPort)
	if err := email.SendWithTLS(addr, smtpHost, auth, smtpFrom, recipients, msg); err != nil {
		slog.Warn("failed to send alert email", "error", err)
	} else {
		slog.Info("alert email sent", "event", event, "recipients", len(recipients))
	}
}
