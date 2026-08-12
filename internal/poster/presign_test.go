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

// PresignGetObject sets no ResponseContentType, so the browser sees whatever
// Content-Type the object carries in S3. Signing a .svg or .html would hand a
// user active content from the S3 origin — the stored-XSS shape the SVG
// artifact was removed to close. Nothing writes those objects now; this keeps
// the property true structurally rather than by what the producer happens to
// emit.
func TestPresignRejectsKeysThatAreNotPNG(t *testing.T) {
	for _, key := range []string{
		"posters/v2/u-x/khruangbin/the-fillmore-2026-08-15-abc1234567.svg",
		"posters/v2/u-x/a/b.html",
		"posters/v2/u-x/a/b.xhtml",
		"posters/v2/u-x/a/b.js",
		"posters/v2/u-x/a/b.json", // the sidecar is the Lambda's, never signed here
		"posters/v2/u-x/a/b",      // no extension at all
		"posters/v2/u-x/a/b.png.svg",
	} {
		if _, err := poster.ValidateKey(key); !errors.Is(err, poster.ErrKeyNotPNG) {
			t.Errorf("ValidateKey(%q) = %v, want ErrKeyNotPNG", key, err)
		}
	}
}

func TestPresignAcceptsAPosterKey(t *testing.T) {
	const key = "posters/v2/u-550e8400-e29b-41d4-a716-446655440000/khruangbin/the-fillmore-2026-08-15-abc1234567.png"
	got, err := poster.ValidateKey(key)
	if err != nil {
		t.Fatalf("ValidateKey(%q) returned %v", key, err)
	}
	if got != key {
		t.Errorf("ValidateKey returned %q, want %q", got, key)
	}
}

var _ = context.Background
