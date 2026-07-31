package handlers

import (
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// errBadCursor is what every decodeCursor failure returns. Callers only ever
// need to distinguish "usable cursor" from "not one", so the specific reason a
// token was rejected is deliberately not surfaced — telling a client which byte
// of an opaque token offended us invites them to start constructing their own.
var errBadCursor = errors.New("malformed cursor")

// encodeCursor packs a keyset position into one opaque token.
//
// Both halves are required: starts_at is not unique (events routinely share a
// start instant), so a cursor carrying only the timestamp would skip or repeat
// rows sitting on a page boundary. The pair (starts_at, id) is unique because id
// is the events primary key.
//
// The encoding is an implementation detail. Nothing outside this file may parse
// it, which is what leaves us free to change it without breaking clients.
func encodeCursor(startsAt time.Time, eventID pgtype.UUID) string {
	raw := startsAt.UTC().Format(time.RFC3339Nano) + "|" + uuidString(eventID)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor reverses encodeCursor. Every failure is client error, never
// server error — see the 400 in parseCursor.
func decodeCursor(s string) (time.Time, uuid.UUID, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.UUID{}, errBadCursor
	}
	tsPart, idPart, found := strings.Cut(string(b), "|")
	if !found {
		return time.Time{}, uuid.UUID{}, errBadCursor
	}
	startsAt, err := time.Parse(time.RFC3339Nano, tsPart)
	if err != nil {
		return time.Time{}, uuid.UUID{}, errBadCursor
	}
	eventID, err := uuid.Parse(idPart)
	if err != nil {
		return time.Time{}, uuid.UUID{}, errBadCursor
	}
	return startsAt, eventID, nil
}
