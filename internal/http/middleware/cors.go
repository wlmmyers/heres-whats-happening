package middleware

import (
	"net/http"
)

// CORS returns a middleware that adds CORS headers when the request's Origin
// matches one of the configured allowed origins. Requests without an Origin
// header pass through unchanged (so server-to-server calls and tests still
// work). OPTIONS preflight requests short-circuit with 204.
//
// Headers exposed to the browser: Authorization (for the Bearer access
// token); the refresh-token cookie lives at Path=/auth and is set/read by
// the API directly, so it doesn't need CORS exposure.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				if _, ok := allowed[origin]; ok {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Access-Control-Allow-Credentials", "true")
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
					w.Header().Set("Vary", "Origin")
				}
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
