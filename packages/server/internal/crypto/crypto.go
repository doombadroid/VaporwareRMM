package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

const encPrefix = "enc:"

var encKey []byte

func init() {
	keyB64 := os.Getenv("SECRETS_ENCRYPTION_KEY")
	if keyB64 == "" {
		slog.Warn("SECRETS_ENCRYPTION_KEY not set — secrets stored unencrypted in DB")
		return
	}
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil || len(key) != 32 {
		slog.Error("SECRETS_ENCRYPTION_KEY must be a base64-encoded 32-byte key")
		return
	}
	encKey = key
}

// Enabled reports whether encryption is configured.
func Enabled() bool { return encKey != nil }

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
