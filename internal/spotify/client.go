package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAuthBase = "https://accounts.spotify.com"
	defaultAPIBase  = "https://api.spotify.com"
	scopes          = "user-top-read user-read-recently-played user-library-read"
)

// Client is the Spotify OAuth + Web API client. Stateless; one instance can
// serve many users.
type Client struct {
	clientID     string
	clientSecret string
	redirectURI  string
	baseURL      string // overrides BOTH auth + api for tests
	http         *http.Client
}

// New builds a Client. If baseURL is empty, production Spotify endpoints are
// used. In tests, pass an httptest.Server URL — the client routes both
// /api/token and /v1/* through it.
func New(clientID, clientSecret, redirectURI, baseURL string) *Client {
	return &Client{
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURI:  redirectURI,
		baseURL:      baseURL,
		http:         &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) authBase() string {
	if c.baseURL != "" {
		return c.baseURL
	}
	return defaultAuthBase
}

func (c *Client) apiBase() string {
	if c.baseURL != "" {
		return c.baseURL
	}
	return defaultAPIBase
}

// AuthorizeURL returns the Spotify authorize URL the user must visit.
func (c *Client) AuthorizeURL(state, codeChallenge string) string {
	q := url.Values{}
	q.Set("client_id", c.clientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", c.redirectURI)
	q.Set("scope", scopes)
	q.Set("state", state)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	return c.authBase() + "/authorize?" + q.Encode()
}

// Token represents the response from /api/token. On refresh the RefreshToken
// field may be empty — Spotify only rotates it when policy requires.
type Token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
}

// ExchangeCode trades an authorization code (+ PKCE verifier) for a Token.
func (c *Client) ExchangeCode(ctx context.Context, code, verifier string) (*Token, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", c.redirectURI)
	form.Set("client_id", c.clientID)
	form.Set("code_verifier", verifier)
	return c.tokenRequest(ctx, form)
}

// RefreshToken trades a refresh token for a new access token.
func (c *Client) RefreshToken(ctx context.Context, refreshToken string) (*Token, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", c.clientID)
	return c.tokenRequest(ctx, form)
}

func (c *Client) tokenRequest(ctx context.Context, form url.Values) (*Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.authBase()+"/api/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token endpoint %d: %s", resp.StatusCode, string(body))
	}
	var t Token
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &t, nil
}

// Artist is one entry in GetTopArtists.
type Artist struct {
	Name   string   `json:"name"`
	Genres []string `json:"genres"`
}

// GetTopArtists returns the user's top artists (long_term, max 50).
func (c *Client) GetTopArtists(ctx context.Context, accessToken string, limit int) ([]Artist, error) {
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	endpoint := fmt.Sprintf("%s/v1/me/top/artists?time_range=long_term&limit=%s",
		c.apiBase(), strconv.Itoa(limit))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("top artists %d: %s", resp.StatusCode, string(body))
	}
	var payload struct {
		Items []Artist `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return payload.Items, nil
}

// GetTopTracks returns the artists drawn from the user's top tracks
// (long_term, max 50 tracks). Spotify nests a simplified artist object on
// each track — it carries a name but no genres — so the returned Artists
// have empty Genres.
func (c *Client) GetTopTracks(ctx context.Context, accessToken string, limit int) ([]Artist, error) {
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	endpoint := fmt.Sprintf("%s/v1/me/top/tracks?time_range=long_term&limit=%s",
		c.apiBase(), strconv.Itoa(limit))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("top tracks %d: %s", resp.StatusCode, string(body))
	}
	var payload struct {
		Items []struct {
			Artists []Artist `json:"artists"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	var artists []Artist
	for _, t := range payload.Items {
		artists = append(artists, t.Artists...)
	}
	return artists, nil
}

// SavedTrackArtist is an artist drawn from one of the user's saved tracks,
// tagged with the track's added_at so callers can rank by recency. Like the
// simplified track artists from GetTopTracks, it carries a name but no genres.
type SavedTrackArtist struct {
	Name    string
	AddedAt time.Time
}

// GetSavedTrackArtists pages through the user's saved tracks ("/me/tracks"),
// 50 at a time, following each response's `next` URL until it is empty. It
// accumulates the artist(s) behind every saved track tagged with that track's
// added_at; the track details themselves are discarded. Dedup and ranking are
// left to the caller.
func (c *Client) GetSavedTrackArtists(ctx context.Context, accessToken string) ([]SavedTrackArtist, error) {
	next := fmt.Sprintf("%s/v1/me/tracks?limit=50", c.apiBase())
	var out []SavedTrackArtist
	for next != "" {
		page, nextURL, err := c.savedTracksPage(ctx, accessToken, next)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		next = nextURL
	}
	return out, nil
}

// savedTracksPage fetches a single page of /me/tracks at the given URL,
// returning that page's artists and the `next` URL ("" when there are no more
// pages).
func (c *Client) savedTracksPage(ctx context.Context, accessToken, pageURL string) ([]SavedTrackArtist, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("saved tracks %d: %s", resp.StatusCode, string(body))
	}
	var payload struct {
		Next  string `json:"next"`
		Items []struct {
			AddedAt time.Time `json:"added_at"`
			Track   struct {
				Artists []Artist `json:"artists"`
			} `json:"track"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, "", fmt.Errorf("decode: %w", err)
	}
	var page []SavedTrackArtist
	for _, it := range payload.Items {
		for _, ar := range it.Track.Artists {
			page = append(page, SavedTrackArtist{Name: ar.Name, AddedAt: it.AddedAt})
		}
	}
	return page, payload.Next, nil
}
