package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	hs "github.com/wmyers/heres-whats-happening/internal/http"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func TestServer_EndToEnd_SignupLoginMe(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)

	s := &hs.Server{
		DB:            pool,
		Queries:       q,
		JWTSigner:     signer,
		RefreshTTL:    time.Hour,
		DefaultCityID: uuid.UUID(city.ID.Bytes).String(),
	}
	mux := s.Router()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// signup
	body, _ := json.Marshal(map[string]string{"email": "e2e@example.com", "password": "hunter22"})
	resp, err := http.Post(srv.URL+"/auth/signup", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var su struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&su))
	resp.Body.Close()
	require.NotEmpty(t, su.AccessToken)

	// GET /me
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/me", nil)
	req.Header.Set("Authorization", "Bearer "+su.AccessToken)
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp2.StatusCode)
	var me struct {
		Email string `json:"email"`
	}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&me))
	resp2.Body.Close()
	require.Equal(t, "e2e@example.com", me.Email)
}
