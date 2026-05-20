package spotify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// SealOAuthState packs state + verifier into a base64url-encoded JSON
// blob and appends an HMAC-SHA256 signature. The full cookie value is
// "base64(json).base64(mac)".
func SealOAuthState(key []byte, state, verifier string, ttl time.Duration) (string, error) {
	payload := struct {
		State    string `json:"s"`
		Verifier string `json:"v"`
		Expires  int64  `json:"e"`
	}{
		State:    state,
		Verifier: verifier,
		Expires:  time.Now().Add(ttl).Unix(),
	}
	body, err := json.Marshal(&payload)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(body)

	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(encoded))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return encoded + "." + sig, nil
}

// OpenOAuthState reverses SealOAuthState, verifying the signature and
// expiration. Returns (state, verifier) on success.
func OpenOAuthState(key []byte, cookie string) (string, string, error) {
	var encoded, sig string
	for i := 0; i < len(cookie); i++ {
		if cookie[i] == '.' {
			encoded, sig = cookie[:i], cookie[i+1:]
			break
		}
	}
	if encoded == "" || sig == "" {
		return "", "", errors.New("malformed cookie")
	}

	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(encoded))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(want)) {
		return "", "", errors.New("bad signature")
	}

	body, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", "", fmt.Errorf("decode: %w", err)
	}
	var payload struct {
		State    string `json:"s"`
		Verifier string `json:"v"`
		Expires  int64  `json:"e"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", "", fmt.Errorf("unmarshal: %w", err)
	}
	if time.Now().Unix() > payload.Expires {
		return "", "", errors.New("expired")
	}
	return payload.State, payload.Verifier, nil
}
