package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/auth"
	emailpkg "github.com/wmyers/heres-whats-happening/internal/email"
	"github.com/wmyers/heres-whats-happening/internal/http/handlers"
	"github.com/wmyers/heres-whats-happening/internal/http/middleware"
	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

func TestGetMe_ReturnsCurrentUser(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)

	signupAndLogin := func(email string) (string, string) {
		body, _ := json.Marshal(map[string]string{"email": email, "password": "hunter22"})
		req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handlers.Signup(q, signer, time.Hour, cityID, handlers.ConfirmationDeps{Sender: &emailpkg.Fake{}})(rec, req)
		require.Equal(t, http.StatusCreated, rec.Code)
		var resp struct {
			AccessToken string `json:"access_token"`
			User        struct{ ID, Email string }
		}
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		return resp.AccessToken, resp.User.ID
	}

	access, _ := signupAndLogin("getme@example.com")

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	rec := httptest.NewRecorder()
	mw := middleware.RequireAuth(signer)
	mw(handlers.GetMe(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var out struct {
		ID     string `json:"id"`
		Email  string `json:"email"`
		CityID string `json:"city_id"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&out))
	require.Equal(t, "getme@example.com", out.Email)
	require.Equal(t, cityID, out.CityID)
}

// DeleteMe removes the users row outright, and the ON DELETE CASCADE foreign
// keys take every table that references it along with it. The old credentials
// and the old refresh cookie must both stop working.
func TestDeleteMe_HardDeletesUserAndCascades(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	creds, _ := json.Marshal(map[string]string{"email": "del@example.com", "password": "hunter22"})
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(creds))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handlers.Signup(q, signer, time.Hour, cityID, handlers.ConfirmationDeps{Sender: &emailpkg.Fake{}})(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var resp struct {
		AccessToken string `json:"access_token"`
		User        struct{ ID string }
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	var refreshCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "refresh_token" {
			refreshCookie = c
		}
	}
	require.NotNil(t, refreshCookie)

	_, err := pool.Exec(ctx, `INSERT INTO user_manual_added_going_events (user_id, show_date, event_name, venue_name)
		VALUES ($1, '2026-01-01', 'A Show', 'A Venue')`, resp.User.ID)
	require.NoError(t, err)

	countRows := func(table string) int {
		var n int
		column := "user_id"
		if table == "users" {
			column = "id"
		}
		require.NoError(t, pool.QueryRow(ctx, "SELECT COUNT(*) FROM "+table+" WHERE "+column+" = $1", resp.User.ID).Scan(&n))
		return n
	}
	tables := []string{"users", "refresh_tokens", "email_confirmations", "user_manual_added_going_events"}
	for _, table := range tables {
		require.Equal(t, 1, countRows(table), "precondition: %s has a row for the user", table)
	}

	req = httptest.NewRequest(http.MethodDelete, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+resp.AccessToken)
	req.AddCookie(refreshCookie)
	rec = httptest.NewRecorder()
	middleware.RequireAuth(signer)(handlers.DeleteMe(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	for _, table := range tables {
		require.Equal(t, 0, countRows(table), "%s still has a row for the deleted user", table)
	}

	// The response clears the refresh cookie, as logout does.
	var cleared bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == "refresh_token" && c.MaxAge < 0 {
			cleared = true
		}
	}
	require.True(t, cleared, "response must clear the refresh_token cookie")

	req = httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(creds))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handlers.Login(q, signer, time.Hour)(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	req = httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	req.AddCookie(refreshCookie)
	rec = httptest.NewRecorder()
	handlers.Refresh(q, signer)(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestGetMe_ReturnsResolvedThreshold(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)
	access, uid := signupForThreshold(t, q, signer, cityID, "getme-th@example.com")

	// Default (NULL) → resolves to the global default (0.3).
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	rec := httptest.NewRecorder()
	mw := middleware.RequireAuth(signer)
	mw(handlers.GetMe(q)).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var out struct {
		ScoreThreshold float64 `json:"score_threshold"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&out))
	require.InDelta(t, 0.3, out.ScoreThreshold, 1e-9)

	// After setting a value, GetMe returns it.
	th := 0.5
	require.NoError(t, q.UpdateUserScoreThreshold(context.Background(), store.UpdateUserScoreThresholdParams{
		ID: pgtype.UUID{Bytes: uuidMust(t, uid), Valid: true}, ScoreThreshold: &th,
	}))
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/me", nil)
	req2.Header.Set("Authorization", "Bearer "+access)
	mw(handlers.GetMe(q)).ServeHTTP(rec2, req2)
	var out2 struct {
		ScoreThreshold float64 `json:"score_threshold"`
	}
	require.NoError(t, json.NewDecoder(rec2.Body).Decode(&out2))
	require.InDelta(t, 0.5, out2.ScoreThreshold, 1e-9)
}

func TestGetMe_ReturnsConfirmed(t *testing.T) {
	q := store.New(testdb.MustOpen(t))
	ctx := context.Background()
	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	row, err := q.CreateUser(ctx, store.CreateUserParams{
		Email: "me-confirmed@example.com", PasswordHash: "x", CityID: city.ID, Confirmed: false,
	})
	require.NoError(t, err)
	uid := uuid.UUID(row.ID.Bytes)

	call := func() map[string]any {
		req := httptest.NewRequest(http.MethodGet, "/me", nil)
		req = req.WithContext(middleware.ContextWithUserID(req.Context(), uid))
		rec := httptest.NewRecorder()
		handlers.GetMe(q)(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
		return body
	}

	require.Equal(t, false, call()["confirmed"])
	require.NoError(t, q.MarkUserConfirmed(ctx, row.ID))
	require.Equal(t, true, call()["confirmed"])
}

func TestSignup_ReturnsCityID(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)

	body, _ := json.Marshal(map[string]string{"email": "citysignup@example.com", "password": "hunter22"})
	req := httptest.NewRequest(http.MethodPost, "/auth/signup", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handlers.Signup(q, signer, time.Hour, cityID, handlers.ConfirmationDeps{Sender: &emailpkg.Fake{}})(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var resp struct {
		User struct {
			CityID string `json:"city_id"`
		} `json:"user"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.NotEmpty(t, resp.User.CityID)
}

func TestGetMe_ReturnsShowSetlists(t *testing.T) {
	pool := testdb.MustOpen(t)
	q := store.New(pool)
	signer := auth.NewJWTSigner("test-key-test-key-test-key-32xx", time.Minute)
	cityID := defaultCityID(t, q)
	access, uid := signupForThreshold(t, q, signer, cityID, "getme-ss@example.com")

	get := func() map[string]any {
		req := httptest.NewRequest(http.MethodGet, "/me", nil)
		req.Header.Set("Authorization", "Bearer "+access)
		rec := httptest.NewRecorder()
		middleware.RequireAuth(signer)(handlers.GetMe(q)).ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var out map[string]any
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&out))
		return out
	}

	// The field must be present and false, not omitted — the client cannot tell
	// an absent key from an opted-out user.
	out := get()
	require.Contains(t, out, "show_setlists")
	require.Equal(t, false, out["show_setlists"])

	require.NoError(t, q.UpdateUserShowSetlists(context.Background(), store.UpdateUserShowSetlistsParams{
		ID: pgtype.UUID{Bytes: uuidMust(t, uid), Valid: true}, ShowSetlists: true,
	}))
	require.Equal(t, true, get()["show_setlists"])
}

// DeleteMe relies on every foreign key into users cascading. A later table that
// references users without ON DELETE CASCADE would make the delete fail its
// foreign key check, and account deletion would 500 for anyone with a row there.
func TestUsersForeignKeys_AllCascadeOnDelete(t *testing.T) {
	pool := testdb.MustOpen(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := pool.Query(ctx, `
		SELECT conrelid::regclass::text, conname, confdeltype::text
		FROM pg_constraint
		WHERE contype = 'f' AND confrelid = 'users'::regclass`)
	require.NoError(t, err)
	defer rows.Close()

	var found int
	for rows.Next() {
		var table, name, delType string
		require.NoError(t, rows.Scan(&table, &name, &delType))
		found++
		require.Equal(t, "c", delType, "%s.%s must be ON DELETE CASCADE", table, name)
	}
	require.NoError(t, rows.Err())
	require.NotZero(t, found, "expected foreign keys referencing users")
}
