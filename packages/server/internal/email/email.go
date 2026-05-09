// Package email centralizes outbound transactional mail.
// All templates live here so handlers can fire-and-forget without
// duplicating SMTP plumbing or message-formatting logic.
package email

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/smtp"
	"os"
	"strings"

	"vaporrmm/server/internal/crypto"
	"vaporrmm/server/internal/db"
)

// smtpConfig is what we pull out of alert_settings for a tenant.
type smtpConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string
}

// loadSMTP returns SMTP settings for the given tenant, falling back to the
// default tenant's row when the per-tenant row is missing or disabled.
func loadSMTP(tenantID string) (smtpConfig, error) {
	if tenantID == "" {
		tenantID = "default"
	}
	cfg, err := loadSMTPRow(tenantID)
	if err == nil {
		return cfg, nil
	}
	// fall back to MSP / 'default' tenant SMTP
	if tenantID != "default" {
		if cfg, err := loadSMTPRow("default"); err == nil {
			return cfg, nil
		}
	}
	return smtpConfig{}, fmt.Errorf("smtp not configured")
}

func loadSMTPRow(tenantID string) (smtpConfig, error) {
	var (
		host, user, encPassword, from string
		port, enabled                 int
	)
	err := db.DB.QueryRow(
		`SELECT smtp_host, smtp_port, smtp_user, smtp_password, smtp_from, enabled FROM alert_settings WHERE tenant_id = ?`,
		tenantID,
	).Scan(&host, &port, &user, &encPassword, &from, &enabled)
	if err != nil {
		return smtpConfig{}, err
	}
	if enabled == 0 || host == "" || from == "" {
		return smtpConfig{}, fmt.Errorf("smtp disabled or incomplete")
	}
	pw, derr := crypto.Decrypt(encPassword)
	if derr != nil {
		slog.Warn("smtp password decrypt failed", "tenant_id", tenantID, "error", derr)
		pw = encPassword
	}
	return smtpConfig{Host: host, Port: port, User: user, Password: pw, From: from}, nil
}

// stripHeaderCRLF removes any CR/LF from a value destined for an email header.
// Without this, an attacker who controls a header value (tenant name, From,
// Subject) can inject additional headers (Bcc, Reply-To) and reroute mail.
func stripHeaderCRLF(s string) string {
	// We replace rather than reject so a typo in a tenant name doesn't break
	// password reset emails. Any \r or \n in headers is universally bad.
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

// Send delivers a plain-text email using the tenant's SMTP config.
// Returns an error if SMTP is not configured for either the tenant or the default fallback.
// All header inputs are stripped of CRLF to prevent header injection.
func Send(tenantID, to, subject, body string) error {
	cfg, err := loadSMTP(tenantID)
	if err != nil {
		return err
	}
	to = stripHeaderCRLF(to)
	subject = stripHeaderCRLF(subject)
	cfg.From = stripHeaderCRLF(cfg.From)
	if to == "" || cfg.From == "" {
		return fmt.Errorf("invalid To/From after sanitization")
	}
	msg := []byte(
		"To: " + to + "\r\n" +
			"From: " + cfg.From + "\r\n" +
			"Subject: " + subject + "\r\n" +
			"Content-Type: text/plain; charset=UTF-8\r\n" +
			"MIME-Version: 1.0\r\n" +
			"\r\n" +
			body + "\r\n",
	)
	var auth smtp.Auth
	if cfg.User != "" && cfg.Password != "" {
		auth = smtp.PlainAuth("", cfg.User, cfg.Password, cfg.Host)
	}
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	return sendWithTLS(addr, cfg.Host, auth, cfg.From, []string{to}, msg)
}

// SendWithTLS is the exported variant of sendWithTLS for callers outside this
// package (events.TriggerEmailAlerts). Same TLS/auth semantics as Send.
func SendWithTLS(addr, host string, auth smtp.Auth, from string, to []string, msg []byte) error {
	return sendWithTLS(addr, host, auth, from, to, msg)
}

// sendWithTLS replaces smtp.SendMail to enforce TLS. Stock SendMail will fall
// back to plaintext silently if the server does not advertise STARTTLS, which
// would leak SMTP credentials and message contents in cleartext. Set
// SMTP_ALLOW_PLAINTEXT=1 to opt out (lab/internal SMTP only).
//
// Port 465 (SMTPS) is implicit-TLS: the connection is wrapped in TLS from
// the start and the server never advertises STARTTLS. We detect that case
// and tls.Dial instead of smtp.Dial.
func sendWithTLS(addr, host string, auth smtp.Auth, from string, to []string, msg []byte) error {
	tlsCfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}

	var c *smtp.Client
	var err error
	if strings.HasSuffix(addr, ":465") {
		conn, derr := tls.Dial("tcp", addr, tlsCfg)
		if derr != nil {
			return fmt.Errorf("smtps dial: %w", derr)
		}
		c, err = smtp.NewClient(conn, host)
		if err != nil {
			conn.Close()
			return fmt.Errorf("smtps client: %w", err)
		}
	} else {
		c, err = smtp.Dial(addr)
		if err != nil {
			return fmt.Errorf("smtp dial: %w", err)
		}
	}
	defer c.Close()

	if err := c.Hello("vaporrmm"); err != nil {
		return fmt.Errorf("smtp hello: %w", err)
	}
	// Implicit-TLS connections (port 465) are already encrypted; only do the
	// STARTTLS dance on submission/relay ports.
	if !strings.HasSuffix(addr, ":465") {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("smtp starttls: %w", err)
			}
		} else if os.Getenv("SMTP_ALLOW_PLAINTEXT") != "1" {
			return fmt.Errorf("smtp server does not advertise STARTTLS; refusing to send in cleartext (set SMTP_ALLOW_PLAINTEXT=1 to override)")
		}
	}
	if auth != nil {
		if ok, _ := c.Extension("AUTH"); ok {
			if err := c.Auth(auth); err != nil {
				return fmt.Errorf("smtp auth: %w", err)
			}
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("smtp mail: %w", err)
	}
	for _, addr := range to {
		if err := c.Rcpt(addr); err != nil {
			return fmt.Errorf("smtp rcpt: %w", err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp data close: %w", err)
	}
	return c.Quit()
}

// SendInvite emails a tenant invite with the accept link.
func SendInvite(tenantID, toEmail, inviterName, tenantName, acceptURL string) error {
	subject := fmt.Sprintf("You've been invited to %s", tenantName)
	body := strings.Join([]string{
		fmt.Sprintf("%s invited you to join %s on vaporRMM.", inviterName, tenantName),
		"",
		"Click the link below to accept and set up your account:",
		acceptURL,
		"",
		"This invitation expires in 7 days.",
		"If you weren't expecting this, ignore this email.",
		"",
		"--",
		"vaporRMM",
	}, "\r\n")
	return Send(tenantID, toEmail, subject, body)
}

// SendPasswordReset emails a password-reset link.
func SendPasswordReset(tenantID, toEmail, resetURL string) error {
	subject := "Reset your vaporRMM password"
	body := strings.Join([]string{
		"Click the link below to reset your password. The link expires in 1 hour.",
		"",
		resetURL,
		"",
		"If you didn't request this, ignore this email.",
		"",
		"--",
		"vaporRMM",
	}, "\r\n")
	return Send(tenantID, toEmail, subject, body)
}
