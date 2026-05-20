package httperr

import (
	"net/http"
	"net/http/httptest"
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
