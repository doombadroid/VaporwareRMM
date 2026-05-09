// Package sysfeatures registers the non-capability dependencies that AI
// capabilities can declare in their DependsOn list. A capability whose deps
// aren't satisfied refuses to be enabled — this is what stops Stage 1 from
// shipping an alert-dedup capability that filters on `device_class` before
// the classification subsystem is reliable.
//
// Each feature has a readiness probe; the probe returns nil when the feature
// is safe to depend on. Probes run at enable time only — they're not on the
// hot path of every AI call.
package sysfeatures

import (
	"errors"
	"strings"

	"vaporrmm/server/internal/ai"
	"vaporrmm/server/internal/db"
)

// Auto-register on import. main.go side-effect imports this package so the
// registry is populated before the first capability registration check.
func init() {
	ai.RegisterSystemFeature("device_classification", deviceClassificationReady)
}

// deviceClassificationReady checks the migration that adds devices.os_class
// has actually run. We don't require any specific number of devices to have
// non-null os_class — capabilities filter on the column themselves and an
// empty match set is acceptable behaviour.
func deviceClassificationReady() error {
	if db.DB == nil {
		return errors.New("device_classification: database not initialised")
	}
	// Probe by attempting a SELECT on the column. Postgres returns a clear
	// error if the column is missing; SQLite returns "no such column".
	var n int
	err := db.DB.QueryRow(`SELECT COUNT(*) FROM devices WHERE os_class IS NOT NULL OR os_class IS NULL`).Scan(&n)
	if err != nil {
		return errors.New("device_classification: devices.os_class column missing — migration 031 not applied")
	}
	return nil
}

// ClassifyOS returns a coarse class for an os_name string emitted by the
// agent. Heuristic, not authoritative — operators who need finer-grained
// classification should use device tags. The heuristic is intentionally
// conservative: when in doubt we return "unknown" rather than guess.
//
// Capabilities that filter by class should treat "unknown" as
// not-eligible-for-action, which is the safer default.
func ClassifyOS(osName string) string {
	o := strings.ToLower(osName)
	switch {
	case strings.Contains(o, "windows server") || strings.Contains(o, "windows-server"):
		return "windows-server"
	case strings.Contains(o, "windows 10") || strings.Contains(o, "windows 11") || strings.Contains(o, "windows7") || strings.Contains(o, "windows 7") || strings.Contains(o, "windows 8"):
		return "windows-workstation"
	case strings.Contains(o, "windows"):
		return "windows-other"
	case strings.HasPrefix(o, "macos") || strings.HasPrefix(o, "mac os") || strings.HasPrefix(o, "darwin"):
		return "mac"
	case strings.Contains(o, "ubuntu server") || strings.Contains(o, "ubuntu-server") ||
		strings.Contains(o, "debian server") ||
		strings.Contains(o, "rhel") || strings.Contains(o, "red hat enterprise") ||
		strings.Contains(o, "centos") || strings.Contains(o, "rocky") || strings.Contains(o, "alma") ||
		strings.Contains(o, "suse") || strings.Contains(o, "fedora server"):
		return "linux-server"
	case strings.Contains(o, "linux") || strings.Contains(o, "ubuntu") || strings.Contains(o, "debian") ||
		strings.Contains(o, "fedora") || strings.Contains(o, "arch") || strings.Contains(o, "gentoo") ||
		strings.Contains(o, "mint") || strings.Contains(o, "manjaro") || strings.Contains(o, "pop"):
		return "linux-workstation"
	case strings.Contains(o, "freebsd") || strings.Contains(o, "openbsd") || strings.Contains(o, "netbsd"):
		return "bsd"
	default:
		return "unknown"
	}
}

// LooksLikeDomainController is a hostname-pattern heuristic — operators who
// need precision should tag DCs explicitly. This catches the common naming
// conventions (DCxx, AD-DCxx, PDCxx) without being overzealous. False
// positives are acceptable on the WRITE side because capabilities that
// exclude DCs treat any match as a hard block; false negatives are not OK
// (we'd auto-act on a DC), so the matcher is biased toward including more.
func LooksLikeDomainController(hostname string) bool {
	h := strings.ToLower(hostname)
	for _, p := range []string{"dc-", "-dc", "dc0", "dc1", "dc2", "dc3", "dc4", "pdc", "bdc", "ad-", "-ad", "domaincontroller"} {
		if strings.Contains(h, p) {
			return true
		}
	}
	if strings.HasPrefix(h, "dc") && len(h) <= 6 {
		return true
	}
	return false
}
