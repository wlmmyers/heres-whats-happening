package httperr

import (
	"bytes"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	Write(rec, http.StatusUnauthorized, "invalid_credentials", "email or password is wrong")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.JSONEq(t,
		`{"error":{"code":"invalid_credentials","message":"email or password is wrong"}}`,
		rec.Body.String())
}

func TestWriteErr_LogsUnderlyingErrorButHidesItFromClient(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/me/calendar?from=x&to=y", nil)
	WriteErr(rec, req, http.StatusInternalServerError, "db_error", "could not load calendar",
		errors.New("dial tcp: connection refused"))

	// Client gets only the generic envelope — no internal detail leaks.
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.JSONEq(t,
		`{"error":{"code":"db_error","message":"could not load calendar"}}`,
		rec.Body.String())

	// Server log captured the underlying cause plus request context.
	logged := buf.String()
	require.Contains(t, logged, "dial tcp: connection refused")
	require.Contains(t, logged, "db_error")
	require.Contains(t, logged, "GET")
	require.Contains(t, logged, "/me/calendar")
}

func TestWriteErr_NilErrorLogsNothing(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	WriteErr(rec, req, http.StatusInternalServerError, "db_error", "boom", nil)

	require.Empty(t, buf.String())
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.JSONEq(t, `{"error":{"code":"db_error","message":"boom"}}`, rec.Body.String())
}
