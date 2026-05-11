package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
)

// Sunshine credentials persistence + rotation.
//
// Codex #2: CI-built agents previously baked in the literal username
// "vaporrmm" and password "vaporrmm" via sunshine_unix.go and
// sunshine_windows.go's configureSunshine("vaporrmm") call. Every
// agent shipped from CI exposed a known-credentialed Sunshine web UI.
//
// Replacement: per-device random credentials, generated on first
// start, persisted to the same protected directory the agent token
// lives in, never logged. startSunshineHidden refuses to launch if
// the persisted credentials are the legacy default. Operators who
// already deployed run the agent once with VAPOR_ROTATE_SUNSHINE=1
// to force regeneration; the rotation path is idempotent and
// non-destructive (it overwrites the credentials file in place).

// sunshineCredsRel is the file name (under the agent's config dir)
// that holds the rendered credentials. JSON because both platforms
// already write Sunshine's own credentials.json — we re-use the
// shape but keep the agent-managed copy in a separate file so
// rotating ours doesn't fight with Sunshine's own writes.
const sunshineCredsRel = "sunshine-managed-credentials.json"

// legacySunshinePassword is the value Codex flagged: the literal
// "vaporrmm" string the previous code hard-coded. Any persisted
// credentials matching this value are rotated immediately on read.
const legacySunshinePassword = "vaporrmm"

// agentSunshineUsername is the Sunshine username this agent owns.
// Static is fine — the username isn't the secret; the password is.
// Kept constant so an operator who already paired Moonlight against
// "vaporrmm" doesn't need to re-pair after rotation.
const agentSunshineUsername = "vaporrmm"

// errSunshineDefaultCreds is returned by loadOrGenerateSunshineCreds
// when the only credentials available are the legacy default. The
// caller (startSunshineHidden) refuses to launch Sunshine in this
// state — the operator must remove the file or set
// VAPOR_ROTATE_SUNSHINE=1 to force regeneration.
var errSunshineDefaultCreds = errors.New("sunshine credentials are the legacy default; refusing to launch")

// agentConfigDir returns the directory where agent-managed state
// lives (alongside agent.env on Linux/macOS; the agent's ProgramData
// folder on Windows). It is created with 0700 if missing.
func agentConfigDir() string {
	if runtime.GOOS == "windows" {
		programData := os.Getenv("ProgramData")
		if programData == "" {
			programData = `C:\ProgramData`
		}
		dir := filepath.Join(programData, "vaporrmm")
		_ = os.MkdirAll(dir, 0700)
		return dir
	}
	dir := "/etc/vaporrmm"
	_ = os.MkdirAll(dir, 0700)
	return dir
}

// generateSunshinePassword returns a 32-character base64-URL-encoded
// random string. 24 bytes of entropy is comfortably more than enough
// against any practical brute force; the bound is the file's own
// 0600 permissions, not the password length.
func generateSunshinePassword() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("sunshine: random source: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// sunshineCreds is the on-disk shape. JSON so a future schema bump
// (algorithm, rotated_at) lands cleanly.
type sunshineCreds struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// loadOrGenerateSunshineCreds returns the agent-managed Sunshine
// credentials. If the persisted file is missing, a fresh random
// password is generated and persisted. If the persisted password is
// the legacy "vaporrmm" default AND VAPOR_ROTATE_SUNSHINE!=1, the
// function returns errSunshineDefaultCreds — the operator must opt
// into rotation so a half-broken install doesn't silently re-paint
// itself secure.
//
// Setting VAPOR_ROTATE_SUNSHINE=1 forces a fresh password regardless
// of existing state. Use after a Codex re-scan confirms the old
// password leaked.
func loadOrGenerateSunshineCreds() (sunshineCreds, error) {
	path := filepath.Join(agentConfigDir(), sunshineCredsRel)
	rotate := os.Getenv("VAPOR_ROTATE_SUNSHINE") == "1"

	if !rotate {
		if data, err := os.ReadFile(path); err == nil {
			var c sunshineCreds
			if err := json.Unmarshal(data, &c); err == nil && c.Username != "" && c.Password != "" {
				if c.Password == legacySunshinePassword {
					return sunshineCreds{}, errSunshineDefaultCreds
				}
				return c, nil
			}
			// File exists but is malformed or empty — regenerate
			// rather than fail. The 0600 perms mean only root /
			// SYSTEM wrote it, and root can decide it's garbage.
		}
	}

	pw, err := generateSunshinePassword()
	if err != nil {
		return sunshineCreds{}, err
	}
	c := sunshineCreds{Username: agentSunshineUsername, Password: pw}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return sunshineCreds{}, fmt.Errorf("sunshine: marshal: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return sunshineCreds{}, fmt.Errorf("sunshine: write %s: %w", path, err)
	}
	// Permission tighten in case the file existed with wider perms
	// before — WriteFile only sets perms on creation.
	if err := os.Chmod(path, 0600); err != nil {
		slog.Warn("could not tighten sunshine credentials file perms", "path", path, "error", err)
	}
	slog.Info("sunshine credentials generated", "path", path)
	return c, nil
}
