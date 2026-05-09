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

	"github.com/gofiber/websocket/v2"
	"github.com/google/uuid"
	"vaporrmm/server/internal/crypto"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/email"
	"vaporrmm/server/internal/redis"
)

// WSClientInfo holds metadata for a connected WebSocket client.
type WSClientInfo struct {
	UserID   string
	TenantID string
	Role     string
}

var (
	WSClients = make(map[*websocket.Conn]*WSClientInfo)
	WSMu      sync.RWMutex
)

// wsEnvelope wraps a broadcast so subscribers on other nodes can re-apply
// the filter against THEIR local connections. We can't filter at publish time
// because the publisher doesn't know which clients are connected on other nodes.
type wsEnvelope struct {
	Kind     string          `json:"k"`            // "all" | "filtered"
	TenantID string          `json:"t,omitempty"`  // for "filtered"
	OwnerID  string          `json:"o,omitempty"`  // for "filtered"
	Payload  json.RawMessage `json:"p"`            // the original message
}

// WSBroadcastMessage sends msg to all connected clients (system-level events).
// When Redis is enabled, we publish only and let our own subscriber fan out
// locally (otherwise local clients would receive duplicates: once direct, once
// via the Redis loopback). When Redis is disabled, we fan out directly.
func WSBroadcastMessage(msg map[string]interface{}) {
	payload, err := json.Marshal(msg)
	if err != nil {
		return
	}
	if redis.IsEnabled() {
		env, err := json.Marshal(wsEnvelope{Kind: "all", Payload: payload})
		if err == nil {
			if err := redis.PublishWSMessage(env); err == nil {
				return
			} else {
				slog.Warn("redis publish failed, falling back to local-only broadcast", "error", err)
			}
		}
	}
	wsBroadcastLocal(payload)
}

// WSBroadcastFiltered sends msg to:
//   - any super_admin (cross-tenant)
//   - admins of the same tenant
//   - the device owner (matching userID)
// Skips clients in other tenants. Same Redis-vs-local dispatch as WSBroadcastMessage.
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
	for conn, info := range WSClients {
		if info.Role == "super_admin" {
			conn.WriteMessage(websocket.TextMessage, data)
			continue
		}
		if info.TenantID != tenantID {
			continue
		}
		if info.Role == "admin" || (ownerID != "" && info.UserID == ownerID) {
			conn.WriteMessage(websocket.TextMessage, data)
		}
	}
}

func wsBroadcastLocal(data []byte) {
	WSMu.RLock()
	defer WSMu.RUnlock()
	for conn := range WSClients {
		conn.WriteMessage(websocket.TextMessage, data)
	}
}

// StartWSRedisSubscriber starts a background goroutine that subscribes to Redis WS
// broadcasts and forwards them to local WebSocket clients.
func StartWSRedisSubscriber() {
	if !redis.IsEnabled() {
		return
	}
	go func() {
		slog.Info("starting redis websocket broadcast subscriber")
		redis.SubscribeWSBroadcast(func(data []byte) {
			// Try to decode as an envelope. Fall back to raw payload for
			// backward compatibility with publishers that haven't been
			// upgraded yet (e.g. during a rolling deploy).
			var env wsEnvelope
			if err := json.Unmarshal(data, &env); err != nil || env.Kind == "" {
				wsBroadcastLocal(data)
				return
			}
			switch env.Kind {
			case "filtered":
				wsFilteredLocal(env.TenantID, env.OwnerID, env.Payload)
			default:
				wsBroadcastLocal(env.Payload)
			}
		})
	}()
}

// AuditLog records an admin action.
// Use AuditLogTenant when tenant_id is known; AuditLog defaults to 'default'.
func AuditLog(userID, action, resourceType, resourceID, details, ipAddress string) {
	AuditLogTenant("default", userID, action, resourceType, resourceID, details, ipAddress)
}

// AuditLogTenant records an admin action scoped to a tenant.
func AuditLogTenant(tenantID, userID, action, resourceType, resourceID, details, ipAddress string) {
	if tenantID == "" {
		tenantID = "default"
	}
	go func() {
		_, err := db.DB.Exec(
			`INSERT INTO audit_logs (id, user_id, action, resource_type, resource_id, details, ip_address, created_at, tenant_id)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			uuid.New().String(), userID, action, resourceType, resourceID, details, ipAddress, time.Now().Unix(), tenantID,
		)
		if err != nil {
			slog.Warn("failed to write audit log", "error", err)
		}
	}()
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
			body, _ := json.Marshal(payload)
			req, _ := http.NewRequest("POST", urlStr, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-VaporRMM-Event", event)
			if plainSecret != "" {
				sig := hmac.New(sha256.New, []byte(plainSecret))
				sig.Write(body)
				req.Header.Set("X-VaporRMM-Signature", hex.EncodeToString(sig.Sum(nil)))
			}

			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Do(req)
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
