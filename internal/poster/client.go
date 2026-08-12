// Package poster talks to the poster-generation Lambda over its AWS_IAM
// Function URL, and mints short-lived read URLs for the artifacts it produces.
package poster

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// The Lambda's own timeout is 300s; allow a little past it so a slow-but-alive
// generation is not cut off by the client first.
const generateTimeout = 310 * time.Second

// maxUpstreamError bounds how much of an upstream body reaches our logs.
const maxUpstreamError = 200

// Request field bounds. These MUST match the Lambda's zod schema
// (MAX_PERFORMER/MAX_VENUE/MAX_DATE in src/poster-schema.ts) and the
// poster_jobs CHECK constraints. A mismatch means the caller gets a 202 and
// then a silently failed job instead of a clean 400.
const (
	MaxPerformer   = 200
	MaxVenue       = 200
	MaxDate        = 100
	MaxRequestBody = 8 << 10
)

type Request struct {
	Performer string `json:"performer"`
	Venue     string `json:"venue"`
	Date      string `json:"date"`
	Force     bool   `json:"force"`
}

// Result is a completed generation. A non-empty FailureStage means the Lambda
// returned a controlled 422 — the job failed, but the call did not.
type Result struct {
	PngKey        string
	Cached        bool
	Artist        json.RawMessage
	Credit        json.RawMessage
	FailureStage  string
	FailureReason string
}

type Generator interface {
	Generate(ctx context.Context, req Request) (Result, error)
}

type Client struct {
	url    string
	region string
	creds  aws.CredentialsProvider
	signer *v4.Signer
	http   *http.Client
}

func NewClient(functionURL, region string, creds aws.CredentialsProvider) *Client {
	return &Client{
		url:    functionURL,
		region: region,
		creds:  creds,
		signer: v4.NewSigner(),
		http:   &http.Client{Timeout: generateTimeout},
	}
}

func (c *Client) Generate(ctx context.Context, req Request) (Result, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return Result{}, fmt.Errorf("marshal poster request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("build poster request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// SigV4 requires the payload hash; the Function URL's auth type is AWS_IAM
	// and its service name is "lambda".
	sum := sha256.Sum256(body)
	creds, err := c.creds.Retrieve(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("retrieve aws credentials: %w", err)
	}
	if err := c.signer.SignHTTP(ctx, creds, httpReq, hex.EncodeToString(sum[:]), "lambda", c.region, time.Now()); err != nil {
		return Result{}, fmt.Errorf("sign poster request: %w", err)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return Result{}, fmt.Errorf("call poster function: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Result{}, fmt.Errorf("read poster response: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		var ok struct {
			PngKey string          `json:"pngKey"`
			Cached bool            `json:"cached"`
			Artist json.RawMessage `json:"artist"`
			Credit json.RawMessage `json:"credit"`
		}
		if err := json.Unmarshal(raw, &ok); err != nil {
			return Result{}, fmt.Errorf("decode poster response: %w", err)
		}
		return Result{PngKey: ok.PngKey, Cached: ok.Cached, Artist: ok.Artist, Credit: ok.Credit}, nil

	case http.StatusUnprocessableEntity:
		// A controlled failure: no MusicBrainz match, no usable image, and so
		// on. Ordinary outcomes here, not transport errors.
		var bad struct {
			Error string `json:"error"`
			Stage string `json:"stage"`
		}
		if err := json.Unmarshal(raw, &bad); err != nil {
			return Result{}, fmt.Errorf("decode poster failure: %w", err)
		}
		return Result{FailureStage: bad.Stage, FailureReason: bad.Error}, nil

	default:
		return Result{}, fmt.Errorf("poster function returned %d: %s", resp.StatusCode, truncate(string(raw)))
	}
}

func truncate(s string) string {
	if len(s) <= maxUpstreamError {
		return s
	}
	return s[:maxUpstreamError] + "…"
}

// JobID is the primary key of a poster job: a digest of the natural key, so a
// POST and a later GET agree without the client carrying an id.
//
// userID is part of that key, not decoration. Jobs are per user: without it,
// one row per show is shared by everyone and POST's force:true — which
// re-claims a ready row — lets any confirmed user blank any other user's
// poster. The accepted cost is that two users wanting the same show each
// generate their own copy.
//
// The values are the DECODED ones. POST reads them from a JSON body and GET
// from a query string via url.Values, which percent-decodes and turns "+" into
// a space; a client that wants a literal "+" must send "%2B". Both handlers
// therefore hash the same string for the same show.
//
// Each field is hashed on its own before the four digests are concatenated
// and hashed again. That makes the encoding unambiguous by construction: a
// fixed-length (32-byte) block can't let bytes from one field bleed into a
// neighboring field the way a separator byte can if that byte turns up
// inside the input. The previous version joined fields with "\x00", but a
// NUL is a legal byte inside a JSON string, so it could be smuggled in
// through the request body and shift where one field ends and the next
// begins — collapsing two distinct natural keys onto one job id.
func JobID(userID, performer, venue, date string) string {
	u := sha256.Sum256([]byte(normalize(userID)))
	p := sha256.Sum256([]byte(normalize(performer)))
	v := sha256.Sum256([]byte(normalize(venue)))
	d := sha256.Sum256([]byte(normalize(date)))

	joined := make([]byte, 0, len(u)+len(p)+len(v)+len(d))
	joined = append(joined, u[:]...)
	joined = append(joined, p[:]...)
	joined = append(joined, v[:]...)
	joined = append(joined, d[:]...)

	sum := sha256.Sum256(joined)
	return hex.EncodeToString(sum[:])
}
