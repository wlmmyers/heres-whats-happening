package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type JWTSigner struct {
	key []byte
	ttl time.Duration
}

func NewJWTSigner(signingKey string, ttl time.Duration) *JWTSigner {
	return &JWTSigner{key: []byte(signingKey), ttl: ttl}
}

// accessClaims is the access token payload. Confirmed rides alongside the
// subject so the confirmation gate costs no database round trip.
//
// This is only sound because confirmation is monotonic: it goes false -> true
// exactly once and never back, so a stale token can only be too restrictive
// (a spurious 403 the client self-heals by refreshing), never too permissive.
// The same technique would be WRONG for a revocable flag such as an admin bit,
// and it would break if a "change your email -> must re-confirm" feature were
// ever added; the fix at that point is to revoke refresh tokens on email change.
type accessClaims struct {
	Confirmed bool `json:"confirmed"`
	jwt.RegisteredClaims
}

func (s *JWTSigner) SignAccess(userID uuid.UUID, confirmed bool) (string, error) {
	now := time.Now()
	claims := accessClaims{
		Confirmed: confirmed,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.ttl)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(s.key)
}

// VerifyAccess returns the user ID and the confirmed claim. Both come from the
// post-verification claims struct — never re-decode the payload segment, which
// is unsigned base64 the caller controls.
func (s *JWTSigner) VerifyAccess(tokenStr string) (uuid.UUID, bool, error) {
	parsed, err := jwt.ParseWithClaims(tokenStr, &accessClaims{}, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.key, nil
	})
	if err != nil {
		return uuid.Nil, false, err
	}
	claims, ok := parsed.Claims.(*accessClaims)
	if !ok || !parsed.Valid {
		return uuid.Nil, false, errors.New("invalid token")
	}
	uid, err := uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil, false, err
	}
	return uid, claims.Confirmed, nil
}
