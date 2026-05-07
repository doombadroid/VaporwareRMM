package events

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
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
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/redis"
)

var (
	WSClients = make(map[*websocket.Conn]bool)
	WSMu      sync.RWMutex
)

func WSBroadcastMessage(msg map[string]interface{}) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	// Publish to Redis for multi-node broadcast
	if redis.IsEnabled() {
		if err := redis.PublishWSMessage(data); err != nil {
			slog.Warn("failed to publish ws message to redis", "error", err)
		}
	}
	// Local broadcast
	wsBroadcastLocal(data)
}

func wsBroadcastLocal(data []byte) {
	WSMu.RLock()
	defer WSMu.RUnlock()
	for client := range WSClients {
		client.WriteMessage(websocket.TextMessage, data)
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
			wsBroadcastLocal(data)
		})
	}()
}

func AuditLog(userID, action, resourceType, resourceID, details, ipAddress string) {
	go func() {
		_, err := db.DB.Exec(
			`INSERT INTO audit_logs (id, user_id, action, resource_type, resource_id, details, ip_address, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			uuid.New().String(), userID, action, resourceType, resourceID, details, ipAddress, time.Now().Unix(),
		)
		if err != nil {
			slog.Warn("failed to write audit log", "error", err)
		}
	}()
}

func TriggerWebhooks(event string, payload map[string]interface{}) {
	go func() {
		rows, err := db.DB.Query(`SELECT id, url, secret, events FROM webhooks WHERE enabled = 1`)
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

			body, _ := json.Marshal(payload)
			req, _ := http.NewRequest("POST", urlStr, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-VaporRMM-Event", event)
			if secret != "" {
				sig := hmac.New(sha256.New, []byte(secret))
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

	go TriggerEmailAlerts(event, payload)
}

func TriggerEmailAlerts(event string, payload map[string]interface{}) {
	var enabled int
	var smtpHost, smtpUser, smtpPassword, smtpFrom string
	var smtpPort int
	err := db.DB.QueryRow(
		`SELECT enabled, smtp_host, smtp_port, smtp_user, smtp_password, smtp_from FROM alert_settings WHERE id = 'default'`,
	).Scan(&enabled, &smtpHost, &smtpPort, &smtpUser, &smtpPassword, &smtpFrom)
	if err != nil || enabled == 0 || smtpHost == "" || smtpFrom == "" {
		return
	}

	rows, err := db.DB.Query(`SELECT email_recipients FROM alert_rules WHERE event_type = ? AND enabled = 1`, event)
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
			recipients = append(recipients, strings.Split(r, ",")...)
		}
	}
	if err := rows.Err(); err != nil {
		slog.Warn("rows iteration error", "error", err)
	}

	if len(recipients) == 0 {
		return
	}

	subject := fmt.Sprintf("[vaporRMM] Alert: %s", event)
	body := fmt.Sprintf("Event: %s\n\nPayload:\n%s\n\n--\nvaporRMM Alert System",
		event,
		func() string {
			b, _ := json.MarshalIndent(payload, "", "  ")
			return string(b)
		}(),
	)

	msg := []byte("To: " + strings.Join(recipients, ", ") + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"\r\n" +
		body + "\r\n")

	var auth smtp.Auth
	if smtpUser != "" && smtpPassword != "" {
		auth = smtp.PlainAuth("", smtpUser, smtpPassword, smtpHost)
	}

	addr := fmt.Sprintf("%s:%d", smtpHost, smtpPort)
	if err := smtp.SendMail(addr, auth, smtpFrom, recipients, msg); err != nil {
		slog.Warn("failed to send alert email", "error", err)
	} else {
		slog.Info("alert email sent", "event", event, "recipients", len(recipients))
	}
}
