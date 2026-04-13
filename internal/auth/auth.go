package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrInvalidToken = errors.New("invalid token")

func RandomToken(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func SignSession(secret, token string, expiresAt time.Time) string {
	payload := token + "|" + expiresAt.UTC().Format(time.RFC3339)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	signature := hex.EncodeToString(mac.Sum(nil))
	return base64.RawURLEncoding.EncodeToString([]byte(payload + "|" + signature))
}

func ParseSession(secret, encoded string) (string, time.Time, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", time.Time{}, ErrInvalidToken
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 {
		return "", time.Time{}, ErrInvalidToken
	}
	payload := parts[0] + "|" + parts[1]
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return "", time.Time{}, ErrInvalidToken
	}
	expiresAt, err := time.Parse(time.RFC3339, parts[1])
	if err != nil {
		return "", time.Time{}, ErrInvalidToken
	}
	if time.Now().After(expiresAt) {
		return "", time.Time{}, ErrInvalidToken
	}
	return parts[0], expiresAt, nil
}
