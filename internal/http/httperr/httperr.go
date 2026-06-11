// Package httperr writes a uniform JSON error envelope.
package httperr

import (
	"encoding/json"
	"log"
	"net/http"

	chimw "github.com/go-chi/chi/v5/middleware"
)

type Body struct {
	Error Payload `json:"error"`
}

type Payload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func Write(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Body{Error: Payload{Code: code, Message: msg}})
}

// WriteErr writes the same client-facing JSON envelope as Write, but also logs
// the underlying error — with the chi request id, method, and path — to the
// server log so the cause of a failure (most importantly a 5xx) is visible in
// the terminal locally and in CloudWatch in prod. The error detail is never
// sent to the client. A nil err logs nothing and behaves exactly like Write.
func WriteErr(w http.ResponseWriter, r *http.Request, status int, code, msg string, err error) {
	if err != nil {
		log.Printf("[%s] %s %s -> %d %s: %v",
			chimw.GetReqID(r.Context()), r.Method, r.URL.Path, status, code, err)
	}
	Write(w, status, code, msg)
}
