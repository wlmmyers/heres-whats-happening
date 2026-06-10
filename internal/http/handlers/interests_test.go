package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	"github.com/wmyers/heres-whats-happening/internal/events"
	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

type fakePublisher struct {
	bodies [][]byte
}

func (f *fakePublisher) Send(_ context.Context, _ string, body []byte) error {
	f.bodies = append(f.bodies, body)
	return nil
}

func signupAndAccess(t *testing.T, q *store.Queries, signer *auth.JWTSigner, cityID, email string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": "hunter22"})
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handlers.Signup(q, signer, time.Hour, cityID)(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var resp struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	return resp.AccessToken
}

func TestPostInterests_CreatesManualTag(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)
	access := signupAndAccess(t, q, signer, cityID, "int1@example.com")

	body, _ := json.Marshal(map[string]string{"value": "Indie Rock"})
	req := httptest.NewRequest(http.MethodPost, "/me/interests", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	mw := middleware.RequireAuth(signer)
	mw(handlers.CreateInterest(q, nil, "")).ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	var out struct {
		ID              string  `json:"id"`
		Value           string  `json:"value"`
		NormalizedValue string  `json:"normalized_value"`
		Weight          float64 `json:"weight"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&out))
	require.Equal(t, "Indie Rock", out.Value)
	require.Equal(t, "indie rock", out.NormalizedValue)
}

func TestPostInterests_DuplicateReturns409(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)
	access := signupAndAccess(t, q, signer, cityID, "int2@example.com")

	send := func() *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"value": "Jazz"})
		req := httptest.NewRequest(http.MethodPost, "/me/interests", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+access)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mw := middleware.RequireAuth(signer)
		mw(handlers.CreateInterest(q, nil, "")).ServeHTTP(rec, req)
		return rec
	}
	require.Equal(t, http.StatusCreated, send().Code)
	require.Equal(t, http.StatusConflict, send().Code)
}

func TestGetInterests_ReturnsOnlyOwn(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)
	access := signupAndAccess(t, q, signer, cityID, "int3@example.com")

	for _, v := range []string{"Rock", "Pop"} {
		body, _ := json.Marshal(map[string]string{"value": v})
		req := httptest.NewRequest(http.MethodPost, "/me/interests", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+access)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mw := middleware.RequireAuth(signer)
		mw(handlers.CreateInterest(q, nil, "")).ServeHTTP(rec, req)
		require.Equal(t, http.StatusCreated, rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/me/interests", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	rec := httptest.NewRecorder()
	mw := middleware.RequireAuth(signer)
	mw(handlers.ListInterests(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var out struct {
		Interests []struct {
			Value string `json:"value"`
		} `json:"interests"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&out))
	require.Len(t, out.Interests, 2)
}

func TestDeleteInterest_OwnershipEnforced(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)
	accessA := signupAndAccess(t, q, signer, cityID, "owner@example.com")
	accessB := signupAndAccess(t, q, signer, cityID, "thief@example.com")

	// owner creates an interest
	body, _ := json.Marshal(map[string]string{"value": "Theater"})
	req := httptest.NewRequest(http.MethodPost, "/me/interests", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+accessA)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mw := middleware.RequireAuth(signer)
	mw(handlers.CreateInterest(q, nil, "")).ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&created))

	// thief tries to delete it
	r := chi.NewRouter()
	r.With(mw).Delete("/me/interests/{id}", handlers.DeleteInterest(q, nil, ""))

	req2 := httptest.NewRequest(http.MethodDelete, "/me/interests/"+created.ID, nil)
	req2.Header.Set("Authorization", "Bearer "+accessB)
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)
	// DELETE is idempotent: thief's call returns 204 but DB row remains owned by A
	require.Equal(t, http.StatusNoContent, rec2.Code)

	// owner can list and still see it
	req3 := httptest.NewRequest(http.MethodGet, "/me/interests", nil)
	req3.Header.Set("Authorization", "Bearer "+accessA)
	rec3 := httptest.NewRecorder()
	mw(handlers.ListInterests(q)).ServeHTTP(rec3, req3)
	require.Equal(t, http.StatusOK, rec3.Code)
	var out struct {
		Interests []struct {
			Value string `json:"value"`
		} `json:"interests"`
	}
	require.NoError(t, json.NewDecoder(rec3.Body).Decode(&out))
	require.Len(t, out.Interests, 1)
	require.Equal(t, "Theater", out.Interests[0].Value)
}

func TestPostInterests_PublishesEmbedMessage(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)
	access := signupAndAccess(t, q, signer, cityID, "pub1@example.com")

	pub := &fakePublisher{}
	body, _ := json.Marshal(map[string]string{"value": "Indie Rock"})
	req := httptest.NewRequest(http.MethodPost, "/me/interests", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	mw := middleware.RequireAuth(signer)
	mw(handlers.CreateInterest(q, pub, "interests-queue-url")).ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	require.Len(t, pub.bodies, 1)
	var msg events.InterestMessage
	require.NoError(t, json.Unmarshal(pub.bodies[0], &msg))
	require.Equal(t, events.OpOnlyEmbed, msg.Op)
	require.NotEmpty(t, msg.UserID)
}

func TestPostInterests_NilPublisher_StillSucceeds(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)
	access := signupAndAccess(t, q, signer, cityID, "pub2@example.com")

	body, _ := json.Marshal(map[string]string{"value": "Jazz"})
	req := httptest.NewRequest(http.MethodPost, "/me/interests", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	mw := middleware.RequireAuth(signer)
	mw(handlers.CreateInterest(q, nil, "")).ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
}
