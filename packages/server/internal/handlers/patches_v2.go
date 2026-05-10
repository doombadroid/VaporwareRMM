package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"vaporrmm/server/internal/auth"
	"vaporrmm/server/internal/db"
	"vaporrmm/server/internal/events"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// agentPatchEntry is one available-update record reported by the agent.
// Source identifies the package manager (apt/dnf/pacman/winupdate/macos);
// kb_id is the package name on unix-like systems and the Microsoft KB
// number (e.g. "KB5034441") on Windows.
type agentPatchEntry struct {
	KBID        string `json:"kb_id"`
	Source      string `json:"source"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Severity    string `json:"severity,omitempty"`
	CVE         string `json:"cve,omitempty"`
}

const (
	maxAgentPatchEntries = 5000
	maxPatchTitleLen     = 256
	maxPatchDescLen      = 4096
	maxPatchKBLen        = 128
)

// allowedPatchSources gates BOTH agent-reported sources (sync endpoint)
// and install-command templating. "manual" is excluded from agent
// reports below — admin can still manually create a patch via the older
// POST /devices/:id/patches with no source/kb_id, but the agent
// should never claim it discovered something via "manual".
var allowedPatchSources = map[string]bool{
	"apt":       true,
	"dnf":       true,
	"yum":       true,
	"pacman":    true,
	"winupdate": true,
	"macos":     true,
	"flatpak":   true,
	"snap":      true,
}

var allowedPatchSeveritiesV2 = map[string]bool{
	"low": true, "medium": true, "high": true, "critical": true,
}

// stripCtl strips ASCII control characters except tab. Same intent as
// inventory.go's truncateString minus the truncation step.
func stripCtl(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\t' || (r >= 0x20 && r != 0x7F) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func clip(s string, max int) string {
	s = stripCtl(s)
	if len(s) > max {
		s = s[:max]
	}
	return s
}

// installCommandFor templates an OS-specific install command from a
// trusted (source, kb_id) pair. Server is the single source of truth for
// what command goes to the agent — API callers cannot inject raw shell.
// Returns the agent command-type string and the JSON payload.
func installCommandFor(source, kbID string) (cmdType string, payload string, err error) {
	source = strings.ToLower(strings.TrimSpace(source))
	if !allowedPatchSources[source] {
		return "", "", fmt.Errorf("unknown source %q", source)
	}
	// Strict regex on kb_id would be ideal; for now we ban shell metas
	// since the agent shells out for unix-like sources. Windows uses
	// PSWindowsUpdate API, no shell concat.
	if strings.ContainsAny(kbID, "`$;|&><\n\r") {
		return "", "", fmt.Errorf("kb_id contains shell metas")
	}
	if len(kbID) == 0 || len(kbID) > maxPatchKBLen {
		return "", "", fmt.Errorf("kb_id missing or too long")
	}

	// Command type tells the agent which code path to take. Payload is a
	// small JSON the agent unpacks. The agent does NOT pass payload to a
	// shell verbatim.
	body := map[string]string{"source": source, "kb_id": kbID}
	pl, _ := json.Marshal(body)
	return "patch_install", string(pl), nil
}

// RegisterPatchV2Routes wires the agent sync endpoint plus the user-side
// install trigger. The basic /patches list and /devices/:id/patches CRUD
// continue to live in patches.go and devices.go.
func RegisterPatchV2Routes(app *fiber.App, api fiber.Router) {
	// Agent endpoint — token-bound device check identical to inventory.
	app.Post("/agent/patches/sync/:id", auth.AgentAuthMiddleware(), func(c *fiber.Ctx) error {
		urlDeviceID := c.Params("id")
		boundDeviceID, _ := c.Locals("device_id").(string)
		if urlDeviceID == "" || boundDeviceID == "" || urlDeviceID != boundDeviceID {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "device id mismatch"})
		}
		deviceID := boundDeviceID

		var req struct {
			Patches []agentPatchEntry `json:"patches"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid body"})
		}
		if len(req.Patches) > maxAgentPatchEntries {
			req.Patches = req.Patches[:maxAgentPatchEntries]
		}

		var tenantID string
		if err := db.DB.QueryRow(`SELECT COALESCE(tenant_id,'default') FROM devices WHERE id = ?`, deviceID).Scan(&tenantID); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Device not found"})
		}
		now := time.Now().Unix()

		// Insert-only with ON CONFLICT DO NOTHING. We don't auto-mark
		// patches "installed" here — install confirmation comes from the
		// command result path. Agent reporting an upgrade as no-longer-
		// available simply omits it; we keep the row in pending state
		// until the install command flips it.
		insertedCount := 0
		var stmt string
		if db.DB.Dialect == "postgres" {
			stmt = `INSERT INTO patches (id, device_id, title, description, severity, status, created_at, tenant_id, kb_id, cve, source) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (device_id, source, kb_id) DO NOTHING`
		} else {
			stmt = `INSERT OR IGNORE INTO patches (id, device_id, title, description, severity, status, created_at, tenant_id, kb_id, cve, source) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		}
		for _, p := range req.Patches {
			source := strings.ToLower(strings.TrimSpace(p.Source))
			if !allowedPatchSources[source] {
				continue
			}
			kb := clip(p.KBID, maxPatchKBLen)
			if kb == "" {
				continue
			}
			title := clip(p.Title, maxPatchTitleLen)
			if title == "" {
				title = kb
			}
			severity := strings.ToLower(strings.TrimSpace(p.Severity))
			if !allowedPatchSeveritiesV2[severity] {
				severity = "medium"
			}
			res, err := db.DB.Exec(stmt,
				uuid.New().String(),
				deviceID,
				title,
				clip(p.Description, maxPatchDescLen),
				severity,
				"pending",
				now,
				tenantID,
				kb,
				clip(p.CVE, 256),
				source,
			)
			if err != nil {
				slog.Warn("patch sync insert failed", "error", err, "kb", kb)
				continue
			}
			if n, _ := res.RowsAffected(); n > 0 {
				insertedCount++
			}
		}
		return c.JSON(fiber.Map{"message": "Patches synced", "new": insertedCount, "received": len(req.Patches)})
	})

	// User endpoint — admin-gated. Queues a typed agent command rather
	// than letting the caller specify shell.
	api.Post("/patches/:id/install", auth.AdminMiddleware(), func(c *fiber.Ctx) error {
		patchID := c.Params("id")
		tf, tArgs := tenantFilter(c)
		args := append([]interface{}{patchID}, tArgs...)
		var (
			deviceID string
			source   sql.NullString
			kbID     sql.NullString
			status   string
		)
		if err := db.DB.QueryRow(`SELECT device_id, source, kb_id, status FROM patches WHERE id = ?`+tf, args...).Scan(&deviceID, &source, &kbID, &status); err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Patch not found"})
		}
		if status == "installed" {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Patch already installed"})
		}
		if !source.Valid || !kbID.Valid {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Patch missing source/kb_id; cannot install (likely a manually-created entry)"})
		}
		cmdType, payload, err := installCommandFor(source.String, kbID.String)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Cannot template install command: " + err.Error()})
		}
		// Get device tenant for command insertion.
		var deviceTenant string
		if err := db.DB.QueryRow(`SELECT COALESCE(tenant_id,'default') FROM devices WHERE id = ?`, deviceID).Scan(&deviceTenant); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Device lookup failed"})
		}
		cmdID := uuid.New().String()
		if _, err := db.DB.Exec(`INSERT INTO device_commands (id, device_id, type, payload, status, created_at, tenant_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			cmdID, deviceID, cmdType, payload, "pending", time.Now().Unix(), deviceTenant); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to queue install command"})
		}
		// Mark patch as in-flight so re-clicking install doesn't requeue.
		_, _ = db.DB.Exec(`UPDATE patches SET status = ? WHERE id = ?`, "installing", patchID)
		userID, _ := c.Locals("user_id").(string)
		events.AuditLogTenant(deviceTenant, userID, "patch.install_queued", "patch", patchID, fmt.Sprintf("queued install of %s/%s on %s", source.String, kbID.String, deviceID), c.IP())
		return c.JSON(fiber.Map{"message": "Install queued", "command_id": cmdID})
	})

}
