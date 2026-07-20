// Package musicbrainz is a small read-only client for the MusicBrainz web
// service, used to source artist genres (Spotify having deprecated its own
// artist genres field). One instance is safe for concurrent use and shares a
// single ~1 req/sec rate limiter, as MusicBrainz requires.
package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/time/rate"
)

const defaultBaseURL = "https://musicbrainz.org"

// Genre is one crowd-tagged genre for an artist, with its vote count.
type Genre struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// Client calls the MusicBrainz web service.
type Client struct {
	http      *http.Client
	baseURL   string
	userAgent string
	limiter   *rate.Limiter
}

// New builds a Client. If baseURL is "", the production MusicBrainz host is
// used; tests pass an httptest.Server URL. userAgent MUST identify the app and
// a contact — MusicBrainz rejects requests without one.
func New(baseURL, userAgent string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		http:      &http.Client{Timeout: 15 * time.Second},
		baseURL:   baseURL,
		userAgent: userAgent,
		limiter:   rate.NewLimiter(rate.Every(time.Second), 1),
	}
}

// get issues a rate-limited GET and decodes the JSON body into out.
func (c *Client) get(ctx context.Context, path string, out any) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("musicbrainz %d: %s", resp.StatusCode, string(body))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}

// SearchArtist returns the MBID of the best-matching artist for name, or ""
// when there is no match.
func (c *Client) SearchArtist(ctx context.Context, name string) (string, error) {
	q := url.Values{}
	q.Set("query", `artist:"`+name+`"`)
	q.Set("fmt", "json")
	q.Set("limit", "1")
	var payload struct {
		Artists []struct {
			ID string `json:"id"`
		} `json:"artists"`
	}
	if err := c.get(ctx, "/ws/2/artist?"+q.Encode(), &payload); err != nil {
		return "", err
	}
	if len(payload.Artists) == 0 {
		return "", nil
	}
	return payload.Artists[0].ID, nil
}

// GetArtistGenres returns the genres (with vote counts) for an MBID.
func (c *Client) GetArtistGenres(ctx context.Context, mbid string) ([]Genre, error) {
	q := url.Values{}
	q.Set("inc", "genres")
	q.Set("fmt", "json")
	var payload struct {
		Genres []Genre `json:"genres"`
	}
	if err := c.get(ctx, "/ws/2/artist/"+mbid+"?"+q.Encode(), &payload); err != nil {
		return nil, err
	}
	return payload.Genres, nil
}
