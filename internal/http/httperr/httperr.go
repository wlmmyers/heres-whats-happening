// Package httperr writes a uniform JSON error envelope.
package httperr

import (
	"encoding/json"
	"net/http"
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
