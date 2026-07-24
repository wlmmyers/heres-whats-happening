package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/store"
	"github.com/wmyers/heres-whats-happening/internal/testdb"
)

// newUser creates a user with the given confirmed flag and returns its id.
func newUser(t *testing.T, q *store.Queries, email string, confirmed bool) pgtype.UUID {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	city, err := q.GetDefaultCity(ctx)
	require.NoError(t, err)
	row, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        email,
		PasswordHash: "x",
		CityID:       city.ID,
		Confirmed:    confirmed,
	})
	require.NoError(t, err)
	require.Equal(t, confirmed, row.Confirmed)
	return row.ID
}

func TestCreateUser_ConfirmedIsExplicit(t *testing.T) {
	q := store.New(testdb.MustOpen(t))
	ctx := context.Background()

	unconfirmed := newUser(t, q, "unconfirmed@example.com", false)
	confirmed := newUser(t, q, "confirmed@example.com", true)

	a, err := q.GetUserByID(ctx, unconfirmed)
	require.NoError(t, err)
	require.False(t, a.Confirmed)

	b, err := q.GetUserByID(ctx, confirmed)
	require.NoError(t, err)
	require.True(t, b.Confirmed)
}

func TestUpsertEmailConfirmation_ReplacesPriorTokenAndClearsConsumed(t *testing.T) {
	q := store.New(testdb.MustOpen(t))
	ctx := context.Background()
	uid := newUser(t, q, "upsert@example.com", false)

	expires := pgtype.Timestamptz{Time: time.Now().Add(24 * time.Hour), Valid: true}
	require.NoError(t, q.UpsertEmailConfirmation(ctx, store.UpsertEmailConfirmationParams{
		UserID: uid, TokenHash: []byte("hash-one"), ExpiresAt: expires,
	}))
	require.NoError(t, q.ConsumeEmailConfirmation(ctx, uid))

	consumed, err := q.GetEmailConfirmationByHash(ctx, []byte("hash-one"))
	require.NoError(t, err)
	require.True(t, consumed.ConsumedAt.Valid)

	// A resend replaces the token and un-consumes the row.
	require.NoError(t, q.UpsertEmailConfirmation(ctx, store.UpsertEmailConfirmationParams{
		UserID: uid, TokenHash: []byte("hash-two"), ExpiresAt: expires,
	}))

	_, err = q.GetEmailConfirmationByHash(ctx, []byte("hash-one"))
	require.Error(t, err, "the prior token must stop resolving")

	fresh, err := q.GetEmailConfirmationByHash(ctx, []byte("hash-two"))
	require.NoError(t, err)
	require.False(t, fresh.ConsumedAt.Valid)
	require.Equal(t, uid, fresh.UserID)
}

func TestMarkUserConfirmed(t *testing.T) {
	q := store.New(testdb.MustOpen(t))
	ctx := context.Background()
	uid := newUser(t, q, "mark@example.com", false)

	require.NoError(t, q.MarkUserConfirmed(ctx, uid))

	row, err := q.GetUserByID(ctx, uid)
	require.NoError(t, err)
	require.True(t, row.Confirmed)
}

func TestGetActiveRefreshTokenByHash_CarriesConfirmed(t *testing.T) {
	q := store.New(testdb.MustOpen(t))
	ctx := context.Background()
	uid := newUser(t, q, "refresh-join@example.com", false)

	_, err := q.CreateRefreshToken(ctx, store.CreateRefreshTokenParams{
		UserID:    uid,
		TokenHash: []byte("refresh-hash"),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)

	row, err := q.GetActiveRefreshTokenByHash(ctx, []byte("refresh-hash"))
	require.NoError(t, err)
	require.False(t, row.Confirmed)

	require.NoError(t, q.MarkUserConfirmed(ctx, uid))
	row, err = q.GetActiveRefreshTokenByHash(ctx, []byte("refresh-hash"))
	require.NoError(t, err)
	require.True(t, row.Confirmed)
}
