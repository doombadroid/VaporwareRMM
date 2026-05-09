package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

const encPrefix = "enc:"

var encKey []byte

// devModeAllowPlaintext is set when DEV_ALLOW_UNENCRYPTED_SECRETS=1 — solely
// for local SQLite hacking. Production startup MUST fail when the key is
// missing or invalid; silently storing TOTP secrets and provider API keys
// in plaintext was the prior behaviour and is unacceptable.
var devModeAllowPlaintext bool

func init() {
	keyB64 := os.Getenv("SECRETS_ENCRYPTION_KEY")
	devModeAllowPlaintext = os.Getenv("DEV_ALLOW_UNENCRYPTED_SECRETS") == "1"
	if keyB64 == "" {
		if devModeAllowPlaintext {
			slog.Warn("SECRETS_ENCRYPTION_KEY not set — DEV_ALLOW_UNENCRYPTED_SECRETS=1 lets us continue with plaintext secrets. NEVER set this in production.")
			return
		}
		slog.Error("SECRETS_ENCRYPTION_KEY is required. Generate one with: openssl rand -base64 32. To run a local dev server with plaintext secrets, set DEV_ALLOW_UNENCRYPTED_SECRETS=1.")
		os.Exit(1)
	}
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil || len(key) != 32 {
		slog.Error("SECRETS_ENCRYPTION_KEY must be a base64-encoded 32-byte key (e.g. openssl rand -base64 32)")
		os.Exit(1)
	}
	encKey = key
}

// Enabled reports whether encryption is configured. Returns true once init has
// resolved a valid key (or false in the explicit dev opt-out mode).
func Enabled() bool { return encKey != nil }

// MustBeEnabled is for callers that absolutely refuse to write plaintext (TOTP
// secrets, provider API keys). Returns an error in the dev opt-out mode so
// the calling endpoint can fail closed instead of silently downgrading.
func MustBeEnabled() error {
	if encKey != nil {
		return nil
	}
	return errors.New("crypto: SECRETS_ENCRYPTION_KEY is required for this operation")
}

// Encrypt encrypts plaintext with AES-256-GCM.
// Returns plaintext unchanged when no key is configured or input is empty.
func Encrypt(plaintext string) (string, error) {
	if plaintext == "" || encKey == nil {
		return plaintext, nil
	}
	block, err := aes.NewCipher(encKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt decrypts a value produced by Encrypt.
// Returns the value unchanged if it is not encrypted or no key is configured.
func Decrypt(value string) (string, error) {
	if !strings.HasPrefix(value, encPrefix) || encKey == nil {
		return value, nil
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, encPrefix))
	if err != nil {
		return "", fmt.Errorf("decrypt: base64 decode: %w", err)
	}
	block, err := aes.NewCipher(encKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", fmt.Errorf("decrypt: ciphertext too short")
	}
	plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plain), nil
}

// HMACSHA256 returns hex(HMAC-SHA256(SECRETS_ENCRYPTION_KEY, msg)). Used for
// tamper-evident signatures over audit rows. The key is derived from the
// encryption key with a domain-separation tag so an attacker who recovers
// one signature can't reuse it to forge ciphertext (or vice versa). When the
// key is absent (dev opt-out only), falls back to plain SHA-256 — which is
// not tamper-evident but keeps the API from panicking in the dev path.
func HMACSHA256(domain, msg string) string {
	if encKey == nil {
		// Dev opt-out: not tamper-evident. Caller has already accepted
		// plaintext secrets; signature degradation is consistent with that.
		h := sha256.New()
		_, _ = io.WriteString(h, domain+"|"+msg)
		return hexEncode(h.Sum(nil))
	}
	derived := sha256.Sum256(append(encKey, []byte("\x00signature\x00"+domain)...))
	mac := hmac.New(sha256.New, derived[:])
	_, _ = io.WriteString(mac, msg)
	return hexEncode(mac.Sum(nil))
}

func hexEncode(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hexdigits[v>>4]
		out[i*2+1] = hexdigits[v&0x0f]
	}
	return string(out)
}
