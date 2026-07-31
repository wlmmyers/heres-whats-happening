package handlers

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

func TestCursorRoundTrip(t *testing.T) {
	id := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	want := time.Date(2026, 8, 1, 19, 30, 0, 0, time.UTC)

	tok := encodeCursor(want, pgtype.UUID{Bytes: id, Valid: true})
	gotTime, gotID, err := decodeCursor(tok)

	require.NoError(t, err)
	require.True(t, want.Equal(gotTime))
	require.Equal(t, id, gotID)
}

// starts_at carries sub-second precision in Postgres. A cursor that truncated
// it would re-emit or skip the row sitting exactly on a page boundary.
func TestCursorRoundTripPreservesSubSecond(t *testing.T) {
	id := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	want := time.Date(2026, 8, 1, 19, 30, 0, 123456000, time.UTC)

	gotTime, _, err := decodeCursor(encodeCursor(want, pgtype.UUID{Bytes: id, Valid: true}))

	require.NoError(t, err)
	require.True(t, want.Equal(gotTime), "want %s got %s", want, gotTime)
}

// A non-UTC input must come back as the same instant.
func TestCursorNormalizesToUTC(t *testing.T) {
	id := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	loc := time.FixedZone("PDT", -7*60*60)
	want := time.Date(2026, 8, 1, 12, 30, 0, 0, loc)

	gotTime, _, err := decodeCursor(encodeCursor(want, pgtype.UUID{Bytes: id, Valid: true}))

	require.NoError(t, err)
	require.True(t, want.Equal(gotTime))
}

func TestCursorRejectsBadInput(t *testing.T) {
	valid := encodeCursor(time.Now(), pgtype.UUID{Bytes: uuid.New(), Valid: true})

	cases := map[string]string{
		"empty":             "",
		"not base64":        "!!!not-base64!!!",
		"base64 of garbage": base64.RawURLEncoding.EncodeToString([]byte("garbage")),
		"no separator":      base64.RawURLEncoding.EncodeToString([]byte("2026-08-01T19:30:00Z")),
		"bad timestamp":     base64.RawURLEncoding.EncodeToString([]byte("nope|" + uuid.New().String())),
		"bad uuid":          base64.RawURLEncoding.EncodeToString([]byte("2026-08-01T19:30:00Z|nope")),
		"truncated token":   valid[:len(valid)-4],
	}

	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := decodeCursor(in)
			require.ErrorIs(t, err, errBadCursor)
		})
	}
}
