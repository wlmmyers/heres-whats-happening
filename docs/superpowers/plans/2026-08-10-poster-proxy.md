# Poster Proxy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make poster generation reachable only through the authenticated Go API, as an async job, and delete the public unauthenticated CloudFront route.

**Architecture:** The Lambda stops signing and returns S3 object keys. The Go API gains `POST /posters` (claim a `poster_jobs` row, generate in a background goroutine, return 202) and `GET /posters` (poll; presign the stored keys at read time). CloudFront's `/api/poster*` behavior and its Lambda invoke permission are deleted, leaving the Function URL with exactly one caller: the ECS task role.

**Tech Stack:** Go 1.x with chi + pgx/v5 + sqlc, `aws-sdk-go-v2` (`service/s3`, `aws/signer/v4`), TypeScript/Vitest in the Lambda, Terraform.

**Spec:** `docs/superpowers/specs/2026-08-10-poster-proxy-design.md`

## Global Constraints

- **Commit each task.** End every commit message with `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`. A pre-commit hook runs Go and web checks (~60s) — let it finish, never `--no-verify`.
- Go commands run from the repo root; Lambda commands from `lambda/mastra-handler`.
- Go: `go build ./... && go vet ./... && go test ./...` must pass at the end of every task. Lambda: `pnpm test && pnpm typecheck`.
- Lambda relative imports use the `.js` extension even though files are `.ts`.
- Presigned URL expiry stays **3600s**.
- **The bucket comes from Go's own config, never from the Lambda's response.** Only keys are taken from the response.
- **Keys are validated against the `posters/v` prefix before signing** and rejected otherwise.
- The background generation goroutine uses `context.Background()`, **never** the request context.
- No new env vars beyond `POSTER_FUNCTION_URL` and `POSTERS_BUCKET`.
- sqlc: running `sqlc generate` also regenerates `internal/store/models.go` — commit it alongside the new `*.sql.go`.
- New rate-limit endpoint constants are mirrored by hand into `terraform/prod/observability.tf` (see the comment at `internal/http/middleware/ratelimit.go:17-21`).

## File Structure

**Create:**
| File | Responsibility |
|---|---|
| `sql/migrations/0022_poster_jobs.up.sql` / `.down.sql` | The `poster_jobs` table |
| `sql/queries/poster_jobs.sql` | Claim / get / mark-ready / mark-failed |
| `internal/poster/client.go` | SigV4-signed Function URL invoke; maps 200/422/5xx |
| `internal/poster/presign.go` | S3 presign + key prefix validation |
| `internal/poster/client_test.go`, `internal/poster/presign_test.go` | |
| `internal/http/handlers/posters.go` | `CreatePoster` / `GetPoster` HTTP handlers |
| `internal/http/handlers/posters_test.go` | |

**Modify:** `lambda/mastra-handler/src/poster-sink.ts` (+tests), `src/poster-schema.ts`, `src/poster.ts` (+tests), `src/handler.poster.test.ts`, `package.json`, `README.md`, `internal/http/server.go`, `internal/http/middleware/ratelimit.go`, `internal/config/*`, `cmd/app/main.go`, `terraform/prod/frontend.tf`, `terraform/prod/posters.tf`, `terraform/prod/iam.tf`, `terraform/prod/ecs_api.tf`, `terraform/prod/observability.tf`, `go.mod`

---

### Task 1: Lambda returns S3 keys instead of presigned URLs

**Files:**
- Modify: `lambda/mastra-handler/src/poster-sink.ts`, `src/poster-sink.s3.test.ts`, `src/poster-sink.test.ts`, `src/poster-schema.ts`, `src/poster.ts`, `src/poster.test.ts`, `src/handler.poster.test.ts`, `package.json`, `README.md`

**Interfaces:**
- Produces: `PosterArtifacts = { svgKey: string; pngKey: string; artist?: ArtistMatch; credit?: ImageCredit }`. `PosterResult` ok branch becomes `{ ok: true; svgKey; pngKey; cached: boolean; artist?; credit? }`. The 200 body becomes `{ svgKey, pngKey, cached, artist?, credit? }`.

Why: presigned URLs expire at 3600s, so anything that persists them serves dead links. Returning keys lets the Go service sign at read time. It also keeps `posterKeyBase`'s slugging (including the sha256 fallback) in one language.

- [ ] **Step 1: Update the sink tests**

In `src/poster-sink.s3.test.ts`, delete the `vi.mock("@aws-sdk/s3-request-presigner", ...)` block entirely, then replace the URL assertions:

```ts
  it("returns the object KEYS it wrote, not signed urls", async () => {
    const s3 = fakeS3();
    const { svg, png } = await refs();
    const out = await new S3PosterSink(s3, "bkt").put(req, svg, png, provenance);

    expect(out.svgKey).toBe("posters/v1/khruangbin/the-fillmore-2026-08-15.svg");
    expect(out.pngKey).toBe("posters/v1/khruangbin/the-fillmore-2026-08-15.png");
    expect("svgUrl" in out).toBe(false);
    expect("pngUrl" in out).toBe(false);
    expect(out.credit).toEqual(provenance.credit);
  });
```

and in the `find` describe block:

```ts
  it("returns keys and provenance when the sidecar exists", async () => {
    const s3 = fakeS3({ [key]: JSON.stringify(provenance) });
    const hit = await new S3PosterSink(s3, "bkt").find(req);

    expect(hit).not.toBeNull();
    expect(hit!.svgKey).toBe("posters/v1/khruangbin/the-fillmore-2026-08-15.svg");
    expect(hit!.pngKey).toBe("posters/v1/khruangbin/the-fillmore-2026-08-15.png");
    expect(hit!.artist).toEqual(provenance.artist);
  });
```

In `src/poster-sink.test.ts`, the `StubPosterSink` assertions change from `out.svgUrl).toContain("posters/v1/khruangbin")` to `out.svgKey).toBe("posters/v1/khruangbin/the-fillmore-2026-08-15.svg")`, and the `find` test asserts `(await sink.find(req))?.svgKey` is set.

- [ ] **Step 2: Run to verify they fail**

Run: `pnpm vitest run src/poster-sink.s3.test.ts src/poster-sink.test.ts`
Expected: FAIL — `out.svgKey` is undefined.

- [ ] **Step 3: Change the sink**

In `src/poster-sink.ts`: delete the `getSignedUrl` import, the `SIGNED_URL_TTL_SECONDS` constant, and the private `sign()` method. Replace the `PosterArtifacts` interface and the two return paths:

```ts
export interface PosterArtifacts extends PosterProvenance {
  /** S3 object keys. The API service presigns these at read time — storing a
   *  presigned URL anywhere would serve a dead link after its 3600s expiry. */
  svgKey: string;
  pngKey: string;
}
```

In `find`, replace the trailing `const [svgUrl, pngUrl] = await this.sign(base); return { svgUrl, pngUrl, ...provenance };` with:

```ts
    return { svgKey: `${base}.svg`, pngKey: `${base}.png`, ...provenance };
```

In `put`, replace the same trailing pair with the identical return. Apply the same change to both `StubPosterSink.find` and `StubPosterSink.put` (they return `svgKey`/`pngKey` built from `posterKeyBase(req)` instead of `https://stub.local/...` URLs).

- [ ] **Step 4: Thread it through the HTTP boundary**

In `src/poster-schema.ts`, the ok branch of `PosterResult`:

```ts
  | { ok: true; svgKey: string; pngKey: string; cached: boolean; artist?: ArtistMatch; credit?: ImageCredit }
```

In `src/poster.ts`, `posterHttpResponse`'s 200 body:

```ts
      body: JSON.stringify({
        svgKey: result.svgKey,
        pngKey: result.pngKey,
        cached: result.cached,
        artist: result.artist,
        credit: result.credit,
      }),
```

`processPosterRequest` needs no logic change — it already spreads `...hit` and `...artifacts`, both of which now carry keys.

- [ ] **Step 5: Drop the presigner dependency**

Remove `"@aws-sdk/s3-request-presigner"` from `dependencies` in `lambda/mastra-handler/package.json`, then run `pnpm install` so the lockfile updates. Confirm with `grep -rn "s3-request-presigner" src/` that nothing still imports it.

- [ ] **Step 6: Update the remaining tests and the README**

In `src/poster.test.ts` and `src/handler.poster.test.ts`, replace `svgUrl`/`pngUrl` expectations with `svgKey`/`pngKey`, and the workflow stubs' returns accordingly.

In `README.md`, the poster paragraph now reads that the endpoint is internal-only and returns S3 object keys:

```markdown
The Lambda also serves an internal **`POST /api/poster`** endpoint on its AWS_IAM-protected Function URL, reachable only by the API service's ECS task role (see `docs/superpowers/specs/2026-08-10-poster-proxy-design.md`). It takes `{ performer, venue, date, force? }` and returns `{ svgKey, pngKey, cached, artist?, credit? }` — S3 object keys, not URLs, because the API service presigns at read time. Clients use the API's `/posters` endpoints instead.
```

- [ ] **Step 7: Verify and commit**

Run: `pnpm test && pnpm typecheck`
Expected: all pass, typecheck exit 0. Commit the Lambda changes.

---

### Task 2: `poster_jobs` table

**Files:**
- Create: `sql/migrations/0022_poster_jobs.up.sql`, `sql/migrations/0022_poster_jobs.down.sql`, `sql/queries/poster_jobs.sql`
- Modify: `internal/store/` (generated), `internal/store/models.go` (generated)

**Interfaces:**
- Produces, on `*store.Queries`: `ClaimPosterJob(ctx, ClaimPosterJobParams) (PosterJob, error)` (returns `pgx.ErrNoRows` when NOT claimed), `GetPosterJob(ctx, id string) (PosterJob, error)`, `MarkPosterJobReady(ctx, MarkPosterJobReadyParams) error`, `MarkPosterJobFailed(ctx, MarkPosterJobFailedParams) error`.

- [ ] **Step 1: Write the migration**

`sql/migrations/0022_poster_jobs.up.sql`:

```sql
-- Async poster generation jobs. Keyed on the natural (performer, venue, date)
-- so a POST and a later GET agree without the client tracking a job id, and so
-- a repeat request joins the existing job instead of starting a second one.
--
-- svg_key/png_key are S3 OBJECT KEYS, never presigned URLs: a presigned URL
-- expires after an hour, so a stored one would serve a dead link. The API
-- presigns at read time.
CREATE TABLE poster_jobs (
    id             TEXT PRIMARY KEY,
    performer      TEXT NOT NULL,
    venue          TEXT NOT NULL,
    -- Free text, matching the Lambda's contract ("Thursday, August 20").
    date           TEXT NOT NULL,
    status         TEXT NOT NULL CHECK (status IN ('pending', 'ready', 'failed')),
    svg_key        TEXT,
    png_key        TEXT,
    artist         JSONB,
    credit         JSONB,
    failure_stage  TEXT,
    failure_reason TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

`sql/migrations/0022_poster_jobs.down.sql`:

```sql
DROP TABLE poster_jobs;
```

- [ ] **Step 2: Write the queries**

`sql/queries/poster_jobs.sql`:

```sql
-- Claim a job for generation. Returns a row ONLY when this caller won the
-- claim; sqlc surfaces "not claimed" as pgx.ErrNoRows. A job is claimable when
-- it does not exist, previously failed, or is a pending row stranded by a task
-- restart (nothing else would ever clear it, since the goroutine died with the
-- task).
--
-- The cutoff uses sqlc.arg(stale_before) rather than a positional $5: this
-- statement both SETS updated_at and COMPARES it, and sqlc names positional
-- params after the column they touch, so a bare $5 would collide with the
-- assignment and produce a confusing generated field name.
-- name: ClaimPosterJob :one
INSERT INTO poster_jobs (id, performer, venue, date, status, updated_at)
VALUES ($1, $2, $3, $4, 'pending', NOW())
ON CONFLICT (id) DO UPDATE SET
    status         = 'pending',
    svg_key        = NULL,
    png_key        = NULL,
    artist         = NULL,
    credit         = NULL,
    failure_stage  = NULL,
    failure_reason = NULL,
    updated_at     = NOW()
WHERE poster_jobs.status = 'failed'
   OR (poster_jobs.status = 'pending' AND poster_jobs.updated_at < sqlc.arg(stale_before))
RETURNING *;

-- name: GetPosterJob :one
SELECT * FROM poster_jobs WHERE id = $1;

-- name: MarkPosterJobReady :exec
UPDATE poster_jobs
SET status = 'ready', svg_key = $2, png_key = $3, artist = $4, credit = $5,
    failure_stage = NULL, failure_reason = NULL, updated_at = NOW()
WHERE id = $1;

-- name: MarkPosterJobFailed :exec
UPDATE poster_jobs
SET status = 'failed', failure_stage = $2, failure_reason = $3, updated_at = NOW()
WHERE id = $1;
```

- [ ] **Step 3: Generate and inspect**

Run: `sqlc generate`
Then confirm `internal/store/poster_jobs.sql.go` exists and that `internal/store/models.go` gained a `PosterJob` struct. Both must be committed.

- [ ] **Step 4: Verify and commit**

Run: `go build ./... && go vet ./...`
Expected: clean. Commit the migration, the queries, and BOTH generated files.

---

### Task 3: `internal/poster` — SigV4 invoke and S3 presign

**Files:**
- Create: `internal/poster/client.go`, `internal/poster/presign.go`, `internal/poster/client_test.go`, `internal/poster/presign_test.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Produces:
  - `type Request struct { Performer, Venue, Date string; Force bool }`
  - `type Result struct { SvgKey, PngKey string; Cached bool; Artist, Credit json.RawMessage; FailureStage, FailureReason string }` — `Failure*` non-empty means the Lambda returned a controlled 422.
  - `type Generator interface { Generate(ctx context.Context, req Request) (Result, error) }`
  - `func NewClient(functionURL, region string, creds aws.CredentialsProvider) *Client`
  - `type Presigner interface { PresignGet(ctx context.Context, key string) (string, error) }`
  - `func NewPresigner(s3api *s3.Client, bucket string) *S3Presigner`
  - `var ErrKeyOutsidePosterPrefix = errors.New("poster: key outside the posters/v prefix")`

- [ ] **Step 1: Add the dependencies**

Run: `go get github.com/aws/aws-sdk-go-v2/service/s3 github.com/aws/aws-sdk-go-v2/aws/signer/v4`

- [ ] **Step 2: Write the presign test**

`internal/poster/presign_test.go`:

```go
package poster_test

import (
	"context"
	"errors"
	"testing"

	"github.com/wmyers/heres-whats-happening/internal/poster"
)

func TestPresignRejectsKeysOutsideThePosterPrefix(t *testing.T) {
	// The key comes from the Lambda's response. A buggy or compromised Lambda
	// must not be able to make the API sign a URL for arbitrary objects.
	for _, key := range []string{
		"secrets/db-password.txt",
		"../../etc/passwd",
		"postersv1/x.svg",     // near-miss: no slash
		"/posters/v1/x.svg",   // leading slash
		"",
	} {
		if _, err := poster.ValidateKey(key); !errors.Is(err, poster.ErrKeyOutsidePosterPrefix) {
			t.Errorf("ValidateKey(%q) = %v, want ErrKeyOutsidePosterPrefix", key, err)
		}
	}
}

func TestPresignAcceptsAPosterKey(t *testing.T) {
	const key = "posters/v1/khruangbin/the-fillmore-2026-08-15.svg"
	got, err := poster.ValidateKey(key)
	if err != nil {
		t.Fatalf("ValidateKey(%q) returned %v", key, err)
	}
	if got != key {
		t.Errorf("ValidateKey returned %q, want %q", got, key)
	}
}

var _ = context.Background
```

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/poster/...`
Expected: FAIL — package does not compile, `ValidateKey` undefined.

- [ ] **Step 4: Write the presigner**

`internal/poster/presign.go`:

```go
package poster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Presigned GET lifetime. Matches what the Lambda used to mint.
const presignTTL = 3600 * time.Second

// keyPrefix is every poster key's required prefix. posterKeyBase in the Lambda
// emits "posters/v<N>/<performer>/<venue>-<date>.<ext>".
const keyPrefix = "posters/v"

var ErrKeyOutsidePosterPrefix = errors.New("poster: key outside the posters/v prefix")

// ValidateKey guards the one place this service signs on another component's
// say-so. The key arrives in the Lambda's response body; the bucket never does.
func ValidateKey(key string) (string, error) {
	if !strings.HasPrefix(key, keyPrefix) || strings.Contains(key, "..") {
		return "", fmt.Errorf("%w: %q", ErrKeyOutsidePosterPrefix, key)
	}
	return key, nil
}

type Presigner interface {
	PresignGet(ctx context.Context, key string) (string, error)
}

// S3Presigner mints short-lived GET URLs for poster artifacts. The bucket is
// configuration, never taken from a response.
type S3Presigner struct {
	client *s3.PresignClient
	bucket string
}

func NewPresigner(api *s3.Client, bucket string) *S3Presigner {
	return &S3Presigner{client: s3.NewPresignClient(api), bucket: bucket}
}

func (p *S3Presigner) PresignGet(ctx context.Context, key string) (string, error) {
	safe, err := ValidateKey(key)
	if err != nil {
		return "", err
	}
	out, err := p.client.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(p.bucket),
		Key:    aws.String(safe),
	}, s3.WithPresignExpires(presignTTL))
	if err != nil {
		return "", fmt.Errorf("presign %s: %w", safe, err)
	}
	return out.URL, nil
}
```

- [ ] **Step 5: Write the client test**

`internal/poster/client_test.go`:

```go
package poster_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/wmyers/heres-whats-happening/internal/poster"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *poster.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return poster.NewClient(srv.URL, "us-east-1",
		credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""))
}

func TestGenerateSignsTheRequestForLambda(t *testing.T) {
	var gotAuth string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"svgKey":"posters/v1/a/b.svg","pngKey":"posters/v1/a/b.png","cached":false}`))
	})

	if _, err := c.Generate(context.Background(), poster.Request{Performer: "La Luz", Venue: "Neumos", Date: "2026-08-20"}); err != nil {
		t.Fatalf("Generate returned %v", err)
	}
	if gotAuth == "" {
		t.Fatal("no Authorization header: the request was not SigV4-signed")
	}
	// Signing for the wrong service silently yields 403s in production.
	if want := "/us-east-1/lambda/aws4_request"; !contains(gotAuth, want) {
		t.Errorf("Authorization credential scope = %q, want it to contain %q", gotAuth, want)
	}
}

func TestGenerateReturnsKeysOnSuccess(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"svgKey":"posters/v1/a/b.svg","pngKey":"posters/v1/a/b.png","cached":true,"artist":{"mbid":"m"}}`))
	})

	res, err := c.Generate(context.Background(), poster.Request{Performer: "x", Venue: "y", Date: "z"})
	if err != nil {
		t.Fatalf("Generate returned %v", err)
	}
	if res.SvgKey != "posters/v1/a/b.svg" || res.PngKey != "posters/v1/a/b.png" {
		t.Errorf("keys = %q/%q", res.SvgKey, res.PngKey)
	}
	if !res.Cached {
		t.Error("Cached = false, want true")
	}
	if len(res.Artist) == 0 {
		t.Error("Artist was dropped")
	}
}

func TestGenerateMapsA422ToAControlledFailureNotAnError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":"no MusicBrainz match for 'zzz'","stage":"image"}`))
	})

	res, err := c.Generate(context.Background(), poster.Request{Performer: "zzz", Venue: "y", Date: "z"})
	if err != nil {
		t.Fatalf("a controlled 422 must not be an error return, got %v", err)
	}
	if res.FailureStage != "image" {
		t.Errorf("FailureStage = %q, want image", res.FailureStage)
	}
	if res.FailureReason == "" {
		t.Error("FailureReason is empty")
	}
}

func TestGenerateTreatsA5xxAsAnError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`internal boom`))
	})

	if _, err := c.Generate(context.Background(), poster.Request{Performer: "x", Venue: "y", Date: "z"}); err == nil {
		t.Fatal("Generate returned nil error on a 500")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 6: Run to verify it fails**

Run: `go test ./internal/poster/...`
Expected: FAIL — `NewClient`, `Client`, `Request`, `Generate` undefined.

- [ ] **Step 7: Write the client**

`internal/poster/client.go`:

```go
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

type Request struct {
	Performer string `json:"performer"`
	Venue     string `json:"venue"`
	Date      string `json:"date"`
	Force     bool   `json:"force"`
}

// Result is a completed generation. A non-empty FailureStage means the Lambda
// returned a controlled 422 — the job failed, but the call did not.
type Result struct {
	SvgKey        string
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
			SvgKey string          `json:"svgKey"`
			PngKey string          `json:"pngKey"`
			Cached bool            `json:"cached"`
			Artist json.RawMessage `json:"artist"`
			Credit json.RawMessage `json:"credit"`
		}
		if err := json.Unmarshal(raw, &ok); err != nil {
			return Result{}, fmt.Errorf("decode poster response: %w", err)
		}
		return Result{SvgKey: ok.SvgKey, PngKey: ok.PngKey, Cached: ok.Cached, Artist: ok.Artist, Credit: ok.Credit}, nil

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
func JobID(performer, venue, date string) string {
	sum := sha256.Sum256([]byte(normalize(performer) + "\x00" + normalize(venue) + "\x00" + normalize(date)))
	return hex.EncodeToString(sum[:])
}
```

Add to `presign.go` (it already imports `strings`):

```go
// normalize makes the job key insensitive to case and surrounding whitespace,
// so "La Luz " and "la luz" are one job rather than two.
func normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
```

- [ ] **Step 8: Verify and commit**

Run: `go test ./internal/poster/... && go vet ./internal/poster/...`
Expected: PASS. Commit the package, `go.mod`, and `go.sum`.

---

### Task 4: HTTP handlers

**Files:**
- Create: `internal/http/handlers/posters.go`, `internal/http/handlers/posters_test.go`

**Interfaces:**
- Consumes: `poster.Generator`, `poster.Presigner`, `poster.JobID`, `poster.Request`, `poster.Result` (Task 3); `store.Queries` methods from Task 2.
- Produces: `PosterDeps struct { Queries *store.Queries; Generator poster.Generator; Presigner poster.Presigner }`, `func CreatePoster(d PosterDeps) http.HandlerFunc`, `func GetPoster(d PosterDeps) http.HandlerFunc`.

- [ ] **Step 1: Write the failing tests**

`internal/http/handlers/posters_test.go` — the two that carry the most weight are the context test and the claim test:

```go
package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wmyers/heres-whats-happening/internal/poster"
)

// stubGenerator records calls and blocks until released, so a test can assert
// on what happens while generation is still in flight.
type stubGenerator struct {
	mu       sync.Mutex
	calls    int
	release  chan struct{}
	result   poster.Result
	err      error
	sawCtxOK chan bool
}

func (s *stubGenerator) Generate(ctx context.Context, _ poster.Request) (poster.Result, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	if s.release != nil {
		<-s.release
	}
	if s.sawCtxOK != nil {
		s.sawCtxOK <- ctx.Err() == nil
	}
	return s.result, s.err
}

func (s *stubGenerator) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// THE test that matters: the background goroutine must NOT inherit the request
// context, which is cancelled the moment the 202 is written. If it does,
// generation dies instantly and every job fails for no visible reason.
func TestCreatePosterGenerationSurvivesRequestCancellation(t *testing.T) {
	gen := &stubGenerator{release: make(chan struct{}), sawCtxOK: make(chan bool, 1)}
	h := newPosterHandlerForTest(t, gen)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/posters",
		strings.NewReader(`{"performer":"La Luz","venue":"Neumos","date":"2026-08-20"}`)).WithContext(ctx)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}

	cancel() // exactly what the server does once the response is written
	close(gen.release)

	select {
	case alive := <-gen.sawCtxOK:
		if !alive {
			t.Fatal("generation saw a cancelled context: the goroutine inherited the request context")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("generation never ran")
	}
}

func TestCreatePosterDoesNotStartASecondGenerationForAPendingJob(t *testing.T) {
	gen := &stubGenerator{release: make(chan struct{})}
	h := newPosterHandlerForTest(t, gen)

	post := func() int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/posters",
			strings.NewReader(`{"performer":"La Luz","venue":"Neumos","date":"2026-08-20"}`)))
		return rec.Code
	}

	if got := post(); got != http.StatusAccepted {
		t.Fatalf("first POST = %d, want 202", got)
	}
	if got := post(); got != http.StatusAccepted {
		t.Fatalf("second POST = %d, want 202", got)
	}
	close(gen.release)

	if n := gen.callCount(); n != 1 {
		t.Errorf("generation ran %d times, want 1 — the second POST must join the pending job", n)
	}
}

func TestGetPosterPresignsFreshOnEveryCall(t *testing.T) {
	// A ready job must presign at read time. Returning a stored URL would serve
	// a dead link once the 3600s expiry passed.
	first := getPosterURLs(t, "ready")
	second := getPosterURLs(t, "ready")
	if first == second {
		t.Error("two GETs returned identical urls; they must be signed per request")
	}
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return out
}
```

Write the remaining cases in the same file, each asserting one thing: `GET` on an unknown job returns 404; on `pending` returns 202; on `failed` returns 200 with `status`, `failure_stage`, and `failure_reason`; on `ready` returns 200 with `svgUrl`, `pngUrl`, and the persisted `artist`/`credit`; a `POST` whose generation returns a controlled failure marks the row `failed`; a `POST` whose generation errors marks the row `failed` with reason `poster service unavailable` and does not leak the upstream text.

Add the `newPosterHandlerForTest` and `getPosterURLs` helpers, backed by the project's existing test-database helper (`internal/testdb`) so the store calls are real — match how the other handler tests in this package obtain a `*store.Queries`.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/http/handlers/ -run TestCreatePoster -v`
Expected: FAIL — `CreatePoster` undefined.

- [ ] **Step 3: Write the handlers**

`internal/http/handlers/posters.go`. The shape, with the load-bearing parts spelled out:

```go
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/wmyers/heres-whats-happening/internal/http/httperr"
	"github.com/wmyers/heres-whats-happening/internal/poster"
	"github.com/wmyers/heres-whats-happening/internal/store"
)

// stalePendingAfter is how long a pending row may sit before another POST may
// re-claim it. The Lambda's own cap is 300s; past this the goroutine that owned
// the job is gone (task restart) and nothing else will ever clear the row.
const stalePendingAfter = 6 * time.Minute

type PosterDeps struct {
	Queries   *store.Queries
	Generator poster.Generator
	Presigner poster.Presigner
}

type posterRequest struct {
	Performer string `json:"performer"`
	Venue     string `json:"venue"`
	Date      string `json:"date"`
	Force     bool   `json:"force"`
}

func CreatePoster(d PosterDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in posterRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			httperr.Write(w, http.StatusBadRequest, "invalid_body", "request body is not valid JSON")
			return
		}
		if in.Performer == "" || in.Venue == "" || in.Date == "" {
			httperr.Write(w, http.StatusBadRequest, "invalid_body", "performer, venue and date are required")
			return
		}

		id := poster.JobID(in.Performer, in.Venue, in.Date)
		_, err := d.Queries.ClaimPosterJob(r.Context(), store.ClaimPosterJobParams{
			ID: id, Performer: in.Performer, Venue: in.Venue, Date: in.Date,
			StaleBefore: time.Now().Add(-stalePendingAfter),
		})
		switch {
		case err == nil:
			// We won the claim: this request owns the generation.
			startGeneration(d, id, poster.Request{
				Performer: in.Performer, Venue: in.Venue, Date: in.Date, Force: in.Force,
			})
		case errors.Is(err, pgx.ErrNoRows):
			// Someone else already has it in flight, or it is already ready.
			// Either way this caller just polls.
		default:
			httperr.WriteErr(w, r, http.StatusInternalServerError, "poster_claim_failed", "could not queue poster generation", err)
			return
		}

		writeJSON(w, http.StatusAccepted, map[string]any{"status": "pending"})
	}
}

// startGeneration runs the job off the request goroutine.
//
// context.Background() is deliberate and load-bearing: the request context is
// cancelled as soon as the 202 is written, so inheriting it would kill the
// generation immediately. The timeout here is the only bound.
func startGeneration(d PosterDeps, id string, req poster.Request) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
		defer cancel()

		res, err := d.Generator.Generate(ctx, req)
		if err != nil {
			slog.Error("poster generation failed", "job", id, "error", err)
			// The upstream detail is logged, never returned to the client.
			_ = d.Queries.MarkPosterJobFailed(ctx, store.MarkPosterJobFailedParams{
				ID: id, FailureStage: ptr("svg"), FailureReason: ptr("poster service unavailable"),
			})
			return
		}
		if res.FailureStage != "" {
			_ = d.Queries.MarkPosterJobFailed(ctx, store.MarkPosterJobFailedParams{
				ID: id, FailureStage: &res.FailureStage, FailureReason: &res.FailureReason,
			})
			return
		}
		if _, err := poster.ValidateKey(res.SvgKey); err != nil {
			slog.Error("poster returned an unexpected key", "job", id, "error", err)
			_ = d.Queries.MarkPosterJobFailed(ctx, store.MarkPosterJobFailedParams{
				ID: id, FailureStage: ptr("svg"), FailureReason: ptr("poster service returned an unexpected artifact"),
			})
			return
		}
		_ = d.Queries.MarkPosterJobReady(ctx, store.MarkPosterJobReadyParams{
			ID: id, SvgKey: &res.SvgKey, PngKey: &res.PngKey,
			Artist: res.Artist, Credit: res.Credit,
		})
	}()
}
```

`GetPoster` reads `performer`/`venue`/`date` from the query string, computes the same `poster.JobID`, loads the row, and switches on status: `pgx.ErrNoRows` → 404 `{"status":"unknown"}`; `pending` → 202 `{"status":"pending"}`; `failed` → 200 with status/stage/reason; `ready` → presign both keys via `d.Presigner.PresignGet` and return `{status, svgUrl, pngUrl, artist, credit}`. A presign error is a 500 — the artifacts are fine, the fault is local.

Add the small helpers this file needs (`writeJSON`, `ptr[T any](v T) *T`) if the package does not already have equivalents; check first and reuse rather than duplicate.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/http/handlers/ -run Poster -v`
Expected: PASS.

- [ ] **Step 5: Verify and commit**

Run: `go build ./... && go vet ./... && go test ./internal/...`

---

### Task 5: Route wiring, rate limiter, config

**Files:**
- Modify: `internal/http/server.go`, `internal/http/middleware/ratelimit.go`, `internal/config/` (the config struct and its loader), `cmd/app/main.go`

**Interfaces:**
- Consumes: `handlers.PosterDeps`, `handlers.CreatePoster`, `handlers.GetPoster` (Task 4); `poster.NewClient`, `poster.NewPresigner` (Task 3).
- Produces: `Server.PosterGenerator poster.Generator` and `Server.PosterPresigner poster.Presigner`; `middleware.EndpointPosterCreate = "poster_create"`.

- [ ] **Step 1: Add the endpoint constant**

In `internal/http/middleware/ratelimit.go`, add to the const block:

```go
	EndpointPosterCreate = "poster_create"
```

The comment at the top of that block lists which keys are mirrored in terraform. Add `poster_create` to that list in the comment — an allowed call costs real LLM spend, so it belongs in the alarmed subset.

- [ ] **Step 2: Add the Server fields and routes**

In `internal/http/server.go`, add to the `Server` struct near the other integrations:

```go
	PosterGenerator poster.Generator
	PosterPresigner poster.Presigner
```

Add the limiter beside the other authenticated ones:

```go
	// Each allowed call can drive nine LLM requests in the poster Lambda.
	posterCreateLimiter := ratelimit.NewMemory(10, time.Hour)
```

Inside the **authenticated + confirmed** group (the one that already installs `RequireAuth`, the `authedLimiter` net, and `RequireConfirmed`), add:

```go
		posterDeps := handlers.PosterDeps{
			Queries:   s.Queries,
			Generator: s.PosterGenerator,
			Presigner: s.PosterPresigner,
		}
		r.Get("/posters", handlers.GetPoster(posterDeps))
		r.With(middleware.RateLimitByUser(posterCreateLimiter, middleware.EndpointPosterCreate)).
			Post("/posters", handlers.CreatePoster(posterDeps))
```

Place these with the other reads/writes in that group — **below** the `r.Use(...)` lines, per the warning already in that file about chi copying the middleware stack at `Group()`/`With()` time.

- [ ] **Step 3: Add config and wire main**

Add `PosterFunctionURL` and `PostersBucket` to the config struct and its loader, following exactly how the neighbouring string settings (e.g. `IcalBaseURL`) are declared, defaulted, and read from the environment.

In `cmd/app/main.go`, where the other AWS clients are built from the loaded `aws.Config`, construct both and assign them to the server:

```go
	posterGen := poster.NewClient(cfg.PosterFunctionURL, awsCfg.Region, awsCfg.Credentials)
	posterPresigner := poster.NewPresigner(s3.NewFromConfig(awsCfg), cfg.PostersBucket)
```

- [ ] **Step 4: Prove the routes are actually guarded**

Placing a route below the `r.Use(...)` lines is what puts it inside the auth and
confirmation gates — an easy thing to get subtly wrong, and it fails open. Add a
routing test alongside the existing server tests asserting that, against the
real `Router()`:

- `POST /posters` with no `Authorization` header returns **401**, not 202.
- `GET /posters` with no `Authorization` header returns **401**, not 404.
- Both, with a valid token for an **unconfirmed** user, return whatever the
  existing `RequireConfirmed` gate returns for other guarded routes — assert the
  same status the package's existing confirmation-gate test asserts, so the two
  cannot drift.

Follow whatever helper the existing server/routing tests use to build a `Server`
and mint a token; do not construct a parallel harness.

- [ ] **Step 5: Verify and commit**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all pass.

---

### Task 6: Terraform — remove the public route, grant the task role

**Files:**
- Modify: `terraform/prod/frontend.tf`, `terraform/prod/posters.tf`, `terraform/prod/iam.tf`, `terraform/prod/ecs_api.tf`, `terraform/prod/observability.tf`

- [ ] **Step 1: Delete the public poster route**

From `terraform/prod/frontend.tf`, delete: the `ordered_cache_behavior` whose `path_pattern` is `"/api/poster*"`, the `origin` block whose `origin_id` is `"lambda-poster"`, and the `aws_cloudfront_origin_access_control "poster_fn"` resource.

Update the comment currently at `frontend.tf:97-99` — it explains that the distribution-wide 403/404 → `index.html` rewrites also cover `/api/poster*`, which stops being true. Replace its poster clause with a note that the poster endpoint is no longer served through CloudFront; leave the SPA rationale intact.

From `terraform/prod/posters.tf`, delete `aws_lambda_permission.allow_cloudfront_invoke_url` and the comment above it.

- [ ] **Step 2: Grant the ECS task role**

In `terraform/prod/iam.tf`, in the same policy document as the existing `SQSSendReceiveDelete` / `SendTransactionalEmail` statements, add:

```hcl
  statement {
    sid       = "InvokePosterFunctionUrl"
    actions   = ["lambda:InvokeFunctionUrl"]
    resources = [aws_lambda_function.mastra_handler.arn]
    condition {
      test     = "StringEquals"
      variable = "lambda:FunctionUrlAuthType"
      values   = ["AWS_IAM"]
    }
  }

  # Required to SIGN, not merely to fetch: a presigned URL cannot grant more
  # than the signing principal holds. Omitting this is why every poster URL
  # 403'd before the band-image branch fixed the Lambda's own copy of this bug.
  statement {
    sid       = "ReadPosters"
    actions   = ["s3:GetObject"]
    resources = ["${aws_s3_bucket.posters.arn}/*"]
  }
```

- [ ] **Step 3: Pass the two settings to the service**

In `terraform/prod/ecs_api.tf`, alongside the existing `API_BASE_URL` / `ICAL_BASE_URL` entries:

```hcl
    { name = "POSTER_FUNCTION_URL", value = aws_lambda_function_url.mastra_handler.function_url },
    { name = "POSTERS_BUCKET", value = aws_s3_bucket.posters.id },
```

- [ ] **Step 4: Mirror the alarm key**

In `terraform/prod/observability.tf`, add to the same map that holds `manual_interests` and `spotify_exchange`:

```hcl
    poster_create = { threshold = 5, description = "Poster-creation rejections — each allowed call can drive nine LLM requests, so this caps runaway spend from one account." }
```

- [ ] **Step 5: Verify and commit**

Run: `cd terraform/prod && terraform fmt -check && terraform validate`
Expected: fmt exit 0, "Success! The configuration is valid."

`terraform plan` needs AWS credentials and may fail on the state lock locally — that is fine; do not force it. Note in the commit message that `terraform/prod` auto-applies via CodeBuild on merge.

---

### Task 7: Full verification

- [ ] **Step 1: Go suite and vet**

Run: `go build ./... && go vet ./... && go test ./...`

- [ ] **Step 2: Lambda suite**

Run: `cd lambda/mastra-handler && pnpm test && pnpm typecheck`

- [ ] **Step 3: Confirm nothing still expects URLs from the Lambda**

Run: `grep -rn "svgUrl\|pngUrl" lambda/mastra-handler/src/`
Expected: no matches. The Lambda now deals only in keys; `svgUrl`/`pngUrl` exist solely in the Go API's responses.

- [ ] **Step 4: Confirm the public route is gone**

Run: `grep -rn "api/poster\|lambda-poster\|poster_fn" terraform/`
Expected: no matches outside comments.

- [ ] **Step 5: Confirm the presigner cannot be pointed at another bucket**

Run: `grep -n "bucket" internal/poster/presign.go`
Expected: the bucket is only ever the struct field set at construction — never read from a response.

- [ ] **Step 6: Commit any remaining doc updates**

---

## Notes for the implementer

**The context bug is the one to get right.** `startGeneration` must use `context.Background()`. If it inherits the request context, every job dies the instant the 202 is written, and the failure looks like "the Lambda is broken" rather than "we cancelled it". There is a dedicated test for this; do not weaken it.

**`ClaimPosterJob` returning `pgx.ErrNoRows` is success, not failure.** It means another caller already owns the job, or it is already ready. Only a caller that actually receives a row starts generation.

**Do not persist presigned URLs.** They expire at 3600s. The table stores keys; the handler signs per request. A test asserts two GETs produce different URLs.

**Trust boundary.** Keys come from the Lambda, the bucket comes from config. `ValidateKey` runs both before signing and before marking a job ready, so a bad key fails the job instead of producing an unusable URL.

**Still open after this plan**, both recorded in the spec's Out of scope: the LLM-authored SVG is served as `image/svg+xml` without sanitization (finding I5), and `performer`/`venue`/`date` have no length bound.
