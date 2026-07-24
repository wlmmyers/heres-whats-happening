package middleware

import (
	"net/http"

	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
)

// RequireConfirmed rejects users whose access token does not carry
// confirmed:true. Install it inside a RequireAuth group — it reads the claim
// out of the request context and takes no *store.Queries, so the gate is hard
// (enforced at the API, not by a cooperating browser) at zero query cost.
//
// A missing claim is a rejection, not a pass: it means the middleware was
// installed outside a RequireAuth group, and failing closed is the only safe
// reading of "we don't know whether this user is confirmed".
func RequireConfirmed() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			confirmed, ok := ConfirmedFromContext(r.Context())
			if !ok || !confirmed {
				httperr.Write(w, http.StatusForbidden, "confirmation_required",
					"confirm your email address to use this feature")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
