package poster_test

import (
	"context"
	"errors"
	"testing"

	"github.com/wmyers/heres-whats-happening/internal/poster"
)

func TestPresignRejectsKeysOutsideThePosterPrefix(t *testing.T) {
	// The key comes from the Lambda's response. A buggy or compromised Lambda
	// must not be able to make the API sign a URL for arbitrary objects.
	for _, key := range []string{
		"secrets/db-password.txt",
		"../../etc/passwd",
		"postersv1/x.svg",   // near-miss: no slash
		"/posters/v1/x.svg", // leading slash
		"",
	} {
		if _, err := poster.ValidateKey(key); !errors.Is(err, poster.ErrKeyOutsidePosterPrefix) {
			t.Errorf("ValidateKey(%q) = %v, want ErrKeyOutsidePosterPrefix", key, err)
		}
	}
}

func TestPresignAcceptsAPosterKey(t *testing.T) {
	const key = "posters/v1/khruangbin/the-fillmore-2026-08-15.svg"
	got, err := poster.ValidateKey(key)
	if err != nil {
		t.Fatalf("ValidateKey(%q) returned %v", key, err)
	}
	if got != key {
		t.Errorf("ValidateKey returned %q, want %q", got, key)
	}
}

var _ = context.Background
