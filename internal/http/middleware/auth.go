package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
)

type ctxKey int

const userIDKey ctxKey = 1

func RequireAuth(signer *auth.JWTSigner) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authz := r.Header.Get("Authorization")
			if !strings.HasPrefix(authz, "Bearer ") {
				httperr.Write(w, http.StatusUnauthorized, "no_token", "Authorization header missing or malformed")
				return
			}
			tok := strings.TrimPrefix(authz, "Bearer ")
			uid, err := signer.VerifyAccess(tok)
			if err != nil {
				httperr.Write(w, http.StatusUnauthorized, "invalid_token", "access token is not valid")
				return
			}
			ctx := ContextWithUserID(r.Context(), uid)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func UserIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	v := ctx.Value(userIDKey)
	if v == nil {
		return uuid.Nil, false
	}
	uid, ok := v.(uuid.UUID)
	return uid, ok
}

// ContextWithUserID returns ctx carrying uid, the inverse of UserIDFromContext.
// RequireAuth uses it on every authenticated request; tests use it to build a
// request that looks authenticated without minting a JWT.
func ContextWithUserID(ctx context.Context, uid uuid.UUID) context.Context {
	return context.WithValue(ctx, userIDKey, uid)
}
