// Package spotify (under internal/scraper) bridges the user_spotify_tokens
// table → the Spotify Web API → the interests-queue. Stateless apart from
// what's stored in the DB.
package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wmyers/heres-whats-happening/internal/crypto"
	"github.com/wmyers/heres-whats-happening/internal/events"
	"github.com/wmyers/heres-whats-happening/internal/spotify"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

// Publisher matches scraper.Publisher (Send only).
type Publisher interface {
	Send(ctx context.Context, queueURL string, body []byte) error
}

// Adapter scrapes one or all connected users' Spotify data.
type Adapter struct {
	q        *store.Queries
	cipher   *crypto.Cipher
	client   *spotify.Client
	pub      Publisher
	queueURL string
}

func NewAdapter(q *store.Queries, c *crypto.Cipher, client *spotify.Client, pub Publisher, queueURL string) *Adapter {
	return &Adapter{q: q, cipher: c, client: client, pub: pub, queueURL: queueURL}
}

// ScrapeOne fetches one user's top artists/genres and publishes an
// InterestMessage. Refreshes the access token if expired.
func (a *Adapter) ScrapeOne(ctx context.Context, userID pgtype.UUID) error {
	row, err := a.q.GetUserSpotifyTokens(ctx, userID)
	if err != nil {
		return fmt.Errorf("load tokens: %w", err)
	}

	accessToken, err := a.cipher.Decrypt(row.AccessTokenEnc)
	if err != nil {
		return fmt.Errorf("decrypt access: %w", err)
	}

	// Refresh if expired (or about to expire within 30s).
	if row.ExpiresAt.Time.Before(time.Now().Add(30 * time.Second)) {
		refreshToken, err := a.cipher.Decrypt(row.RefreshTokenEnc)
		if err != nil {
			return fmt.Errorf("decrypt refresh: %w", err)
		}
		tok, err := a.client.RefreshToken(ctx, string(refreshToken))
		if err != nil {
			return fmt.Errorf("refresh: %w", err)
		}
		newAT, err := a.cipher.Encrypt([]byte(tok.AccessToken))
		if err != nil {
			return fmt.Errorf("encrypt access: %w", err)
		}
		// Spotify may or may not return a new refresh token. Reuse the old one
		// if it didn't.
		newRT := row.RefreshTokenEnc
		if tok.RefreshToken != "" {
			newRT, err = a.cipher.Encrypt([]byte(tok.RefreshToken))
			if err != nil {
				return fmt.Errorf("encrypt refresh: %w", err)
			}
		}
		if err := a.q.UpsertUserSpotifyTokens(ctx, store.UpsertUserSpotifyTokensParams{
			UserID:          userID,
			AccessTokenEnc:  newAT,
			RefreshTokenEnc: newRT,
			ExpiresAt:       pgtype.Timestamptz{Time: time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second), Valid: true},
			Scope:           tok.Scope,
		}); err != nil {
			return fmt.Errorf("persist refreshed tokens: %w", err)
		}
		accessToken = []byte(tok.AccessToken)
	}

	// Fetch top artists and the artists behind the user's top tracks. These
	// are kept as separate signals: top artists carry genres, track artists
	// don't.
	artists, err := a.client.GetTopArtists(ctx, string(accessToken), 50)
	if err != nil {
		return fmt.Errorf("get top artists: %w", err)
	}
	trackArtists, err := a.client.GetTopTracks(ctx, string(accessToken), 50)
	if err != nil {
		return fmt.Errorf("get top tracks: %w", err)
	}

	msg := events.InterestMessage{
		UserID:    userIDString(userID),
		FetchedAt: time.Now().UTC(),
	}
	msg.SpotifyTopArtists = make([]events.SpotifyTopItem, 0, len(artists))
	genreCount := map[string]int{}
	for i, ar := range artists {
		msg.SpotifyTopArtists = append(msg.SpotifyTopArtists, events.SpotifyTopItem{
			Name: ar.Name,
			Rank: i + 1,
		})
		for _, g := range ar.Genres {
			genreCount[g]++
		}
	}

	// Track artists become their own ranked list, deduped by normalized name —
	// the same key ingest writes to normalized_value — so casing/diacritic
	// variants collapse to one entry (a name's first appearance, i.e. its
	// highest-ranked track, sets its rank).
	seenTrackArtist := make(map[string]bool, len(trackArtists))
	msg.SpotifyTopTrackArtists = make([]events.SpotifyTopItem, 0, len(trackArtists))
	for _, ar := range trackArtists {
		key := events.NormalizeString(ar.Name)
		if key == "" || seenTrackArtist[key] {
			continue
		}
		seenTrackArtist[key] = true
		msg.SpotifyTopTrackArtists = append(msg.SpotifyTopTrackArtists, events.SpotifyTopItem{
			Name: ar.Name,
			Rank: len(msg.SpotifyTopTrackArtists) + 1,
		})
	}

	type gc struct {
		name  string
		count int
	}
	gs := make([]gc, 0, len(genreCount))
	for name, count := range genreCount {
		gs = append(gs, gc{name, count})
	}
	sort.SliceStable(gs, func(i, j int) bool {
		if gs[i].count != gs[j].count {
			return gs[i].count > gs[j].count
		}
		return gs[i].name < gs[j].name
	})
	msg.SpotifyTopGenres = make([]events.SpotifyTopItem, 0, len(gs))
	for i, g := range gs {
		msg.SpotifyTopGenres = append(msg.SpotifyTopGenres, events.SpotifyTopItem{
			Name: g.name,
			Rank: i + 1,
		})
	}

	body, err := json.Marshal(&msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := a.pub.Send(ctx, a.queueURL, body); err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	if err := a.q.UpdateUserSpotifyTokensLastSynced(ctx, userID); err != nil {
		return fmt.Errorf("update last_synced: %w", err)
	}
	return nil
}

// ScrapeAll iterates all users with Spotify tokens and scrapes each.
func (a *Adapter) ScrapeAll(ctx context.Context) []error {
	rows, err := a.q.ListUserSpotifyTokens(ctx)
	if err != nil {
		return []error{fmt.Errorf("list users: %w", err)}
	}
	var errs []error
	for _, r := range rows {
		if err := a.ScrapeOne(ctx, r.UserID); err != nil {
			errs = append(errs, fmt.Errorf("user %s: %w", userIDString(r.UserID), err))
		}
	}
	return errs
}

// userIDString stringifies a pgtype.UUID. Self-contained — no google/uuid dep.
func userIDString(u pgtype.UUID) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 36)
	j := 0
	for i := 0; i < 16; i++ {
		b := u.Bytes[i]
		out[j] = hex[b>>4]
		out[j+1] = hex[b&0x0F]
		j += 2
		switch i {
		case 3, 5, 7, 9:
			out[j] = '-'
			j++
		}
	}
	return string(out)
}
