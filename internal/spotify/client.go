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
	scopes          = "user-top-read user-read-recently-played"
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

// GetTopArtists returns the user's top artists (medium_term, max 50).
func (c *Client) GetTopArtists(ctx context.Context, accessToken string, limit int) ([]Artist, error) {
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	endpoint := fmt.Sprintf("%s/v1/me/top/artists?time_range=medium_term&limit=%s",
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
