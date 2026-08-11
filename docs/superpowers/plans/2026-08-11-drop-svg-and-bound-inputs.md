# Drop SVG Artifact and Bound Inputs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop producing, storing, and serving the LLM-authored SVG — keeping only the PNG — and bound `performer`/`venue`/`date` at every layer that stores or spends on them.

**Architecture:** The substituted SVG string stays as a transient because resvg needs it to make the PNG; everything that *persists* or *exposes* it goes. Length bounds land in three places on purpose — the Go handler (the only public entry), the Lambda's zod schema (what spends the tokens), and CHECK constraints in a new migration.

**Tech Stack:** TypeScript/Vitest in the Lambda, Go with chi + pgx/v5 + sqlc, Postgres.

**Spec:** `docs/superpowers/specs/2026-08-11-drop-svg-artifact-design.md`

## Global Constraints

- **Commit each task.** End every commit message with `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`. A pre-commit hook runs Go and web checks (~60s) — let it finish, never `--no-verify`.
- Go commands run from the repo root; Lambda commands from `lambda/mastra-handler`.
- Every task must end green: `go build ./... && go vet ./... && go test ./...` and, for Lambda tasks, `pnpm test && pnpm typecheck`.
- Lambda relative imports use the `.js` extension even though files are `.ts`.
- **These exact limits, identical in all three layers:** `performer` **200**, `venue` **200**, `date` **100**, request body **8 KB**. A mismatch turns a clean 400 into a silently failed job.
- Bounds are measured on the **trimmed** value.
- The `.json` provenance sidecar STAYS and is still written **last** — it is the commit marker `find()` keys off. Never parallelise the S3 writes; a `maxInFlight` assertion pins this.
- **A NEW migration `0023`.** Never edit `0022` — it is already applied to the local dev database, and `golang-migrate` has no content checksum, so editing it leaves that DB silently stale.
- No new npm or Go dependencies.

## File Structure

**Create:** `sql/migrations/0023_poster_jobs_svg_and_bounds.up.sql` / `.down.sql`

**Modify:**
`lambda/mastra-handler/src/mastra/workflows/compose-poster.step.ts` (+test),
`.../workflows/poster.schemas.ts` (+test), `.../workflows/poster.workflow.test.ts`,
`src/poster-sink.ts` (+`poster-sink.test.ts`, `poster-sink.s3.test.ts`),
`src/poster-schema.ts` (+test), `src/poster.ts` (+test), `src/handler.poster.test.ts`,
`scripts/invoke-poster-local.ts`,
`sql/queries/poster_jobs.sql`, `internal/store/` (generated),
`internal/poster/client.go` (+test), `internal/http/handlers/posters.go` (+test),
`README.md`, and superseding notes in the two prior specs.

---

### Task 1: Lambda — stop producing the SVG artifact

**Files:**
- Modify: `src/mastra/workflows/compose-poster.step.ts`, `src/mastra/workflows/poster.schemas.ts`, `src/mastra/workflows/compose-poster.step.test.ts`, `src/mastra/workflows/poster.workflow.test.ts`, `src/mastra/workflows/poster.schemas.test.ts`

**Interfaces:**
- Produces: `PosterLoopStateSchema` and `PosterWorkflowOutputSchema` both carry `render?: { png: ArtifactRef }`. `authoredSvg` no longer exists on either. Task 2 consumes the narrowed `render`.

- [ ] **Step 1: Write the failing tests**

In `src/mastra/workflows/compose-poster.step.test.ts`, replace the two artifact assertions. **Removal must be asserted positively — a test that merely stops mentioning the SVG proves nothing:**

```ts
  it("writes ONLY a png, and keeps no svg in state", async () => {
    authorGenerate.mockResolvedValue({ object: { svg: GOOD_SVG } });
    rasterizeSvg.mockResolvedValue({ ok: true, png: PNG, width: 1080, height: 1350 });
    critiqueGenerate.mockResolvedValue({ object: { acceptable: true, critique: "bold and legible" } });

    const out = await run(base);

    expect(out.accepted).toBe(true);
    expect(out.render.png.path).toContain(join("run-test", "poster-1.png"));
    expect(out.render.png.bytes).toBe(PNG.byteLength);
    expect(await readFile(out.render.png.path)).toEqual(PNG);

    // The SVG is a transient on the way to the PNG — nothing persists it.
    expect("svg" in out.render).toBe(false);
    expect("authoredSvg" in out).toBe(false);
    const files = await readdir(join(root, "run-test"));
    expect(files.filter((f) => f.endsWith(".svg"))).toEqual([]);
  });
```

Add `readdir` to the `node:fs/promises` import. Delete the existing "keeps the SMALL authored svg in state" test outright — the behavior it pins is being removed. In the remaining failure-path tests, drop every `authoredSvg` assertion and every `authoredSvg:` field from expected objects.

In `src/mastra/workflows/poster.schemas.test.ts`, add:

```ts
describe("PosterLoopStateSchema", () => {
  it("has no place to put an svg", () => {
    const s = PosterLoopStateSchema.parse({
      ...base, imageOk: true, attempts: 0, accepted: false,
      render: { png: { path: "/tmp/p.png", contentType: "image/png", bytes: 20 } },
    });
    expect(s.render?.png.path).toBe("/tmp/p.png");
    expect("authoredSvg" in s).toBe(false);
    expect("svg" in (s.render ?? {})).toBe(false);
  });
});
```

- [ ] **Step 2: Run to verify they fail**

Run: `pnpm vitest run src/mastra/workflows/compose-poster.step.test.ts src/mastra/workflows/poster.schemas.test.ts`
Expected: FAIL — `out.render` still has `svg`, and `authoredSvg` is still present.

- [ ] **Step 3: Narrow the schemas**

In `src/mastra/workflows/poster.schemas.ts`, inside `PosterLoopStateSchema` delete the `authoredSvg` field and change `render` to:

```ts
  render: z.object({ png: ArtifactRefSchema }).optional(),
```

Make the identical `render` change in `PosterWorkflowOutputSchema`.

- [ ] **Step 4: Stop writing the SVG**

In `src/mastra/workflows/compose-poster.step.ts`, replace the render block:

```ts
    // 4) Persist the PNG; state carries a ref only. The substituted SVG was a
    //    transient on the way here — resvg needed it, nothing else does.
    let render;
    try {
      render = { png: await store.write(`poster-${attempts}.png`, raster.png, "image/png") };
    } catch (e) {
      return { ...inputData, attempts, accepted: false, critique: `could not store the render: ${message(e)}` };
    }
```

Then delete `authoredSvg: rawSvg,` from **every** return path in this file — the parse-failure, raster-failure, store-failure, and success returns all set it today. Grep the file for `authoredSvg` afterwards and confirm zero hits.

- [ ] **Step 5: Fix the workflow test's compose stub**

`src/mastra/workflows/poster.workflow.test.ts` mocks `composePosterStep` and returns `render: { svg: {...}, png: {...} }` in at least two places. Change every one to `render: { png: {...} }` and drop any `authoredSvg` it sets.

- [ ] **Step 6: Run tests**

Run: `pnpm test && pnpm typecheck`
Expected: all pass, typecheck exit 0. Commit.

---

### Task 2: Lambda — drop `svgKey` from the sink and HTTP contract, add zod bounds

**Files:**
- Modify: `src/poster-sink.ts`, `src/poster-sink.test.ts`, `src/poster-sink.s3.test.ts`, `src/poster-schema.ts`, `src/poster.ts`, `src/poster.test.ts`, `src/handler.poster.test.ts`, `scripts/invoke-poster-local.ts`

**Interfaces:**
- Consumes: `render: { png: ArtifactRef }` (Task 1).
- Produces: `PosterArtifacts = PosterProvenance & { pngKey: string }`; `PosterSink.put(req, png: ArtifactRef, provenance)`; `PosterResult` ok branch `{ ok: true; pngKey; cached; artist?; credit? }`; 200 body `{ pngKey, cached, artist?, credit? }`. Exported limits `MAX_PERFORMER = 200`, `MAX_VENUE = 200`, `MAX_DATE = 100`.

- [ ] **Step 1: Write the failing tests**

In `src/poster-sink.s3.test.ts`, replace the put/find assertions:

```ts
  it("writes exactly TWO objects: the png, then the sidecar last", async () => {
    const s3 = fakeS3();
    const { png } = await refs();
    await new S3PosterSink(s3, "bkt").put(req, png, provenance);

    const keys = s3.sent
      .filter((c: any) => c.constructor.name === "PutObjectCommand")
      .map((c: any) => c.input.Key as string);
    expect(keys).toEqual([
      "posters/v1/khruangbin/the-fillmore-2026-08-15.png",
      "posters/v1/khruangbin/the-fillmore-2026-08-15.json",
    ]);
    // The sidecar is still the commit marker, so the writes stay sequential.
    expect(s3.maxInFlight).toBe(1);
  });

  it("returns only a png key", async () => {
    const s3 = fakeS3();
    const { png } = await refs();
    const out = await new S3PosterSink(s3, "bkt").put(req, png, provenance);
    expect(out.pngKey).toBe("posters/v1/khruangbin/the-fillmore-2026-08-15.png");
    expect("svgKey" in out).toBe(false);
  });
```

Update the `refs()` helper to build and return only a png ref. In the `find` describe block, assert `hit!.pngKey` is set and `"svgKey" in hit! === false`.

In `src/poster-sink.test.ts`, `StubPosterSink.put` now takes one ref; assert `out.pngKey` and `"svgKey" in out === false`.

Create `src/poster-schema.test.ts` additions (or a new describe if the file exists):

```ts
describe("PosterRequestSchema length bounds", () => {
  const ok = { venue: "V", date: "D" };

  it("accepts a performer at the limit and rejects one over it", () => {
    expect(PosterRequestSchema.safeParse({ ...ok, performer: "a".repeat(200) }).success).toBe(true);
    expect(PosterRequestSchema.safeParse({ ...ok, performer: "a".repeat(201) }).success).toBe(false);
  });

  it("measures the TRIMMED value, so whitespace cannot consume the budget", () => {
    // JobID normalizes by trimming, so these two are the SAME job — they must
    // therefore agree on whether they are acceptable.
    const padded = `  ${"a".repeat(200)}  `;
    expect(PosterRequestSchema.safeParse({ ...ok, performer: padded }).success).toBe(true);
  });

  it("bounds venue at 200 and date at 100", () => {
    expect(PosterRequestSchema.safeParse({ performer: "P", date: "D", venue: "a".repeat(201) }).success).toBe(false);
    expect(PosterRequestSchema.safeParse({ performer: "P", venue: "V", date: "a".repeat(101) }).success).toBe(false);
  });
});
```

In `src/poster.test.ts` and `src/handler.poster.test.ts`, change `svgKey` expectations to `pngKey` and add `expect("svgKey" in body).toBe(false)` to the 200-body test.

- [ ] **Step 2: Run to verify they fail**

Run: `pnpm vitest run src/poster-sink.s3.test.ts src/poster-schema.test.ts`
Expected: FAIL — three objects are still written, and a 201-char performer is still accepted.

- [ ] **Step 3: Narrow the sink**

In `src/poster-sink.ts`:

```ts
export interface PosterArtifacts extends PosterProvenance {
  /** S3 object key. The API service presigns this at read time — storing a
   *  presigned URL anywhere would serve a dead link after its 3600s expiry. */
  pngKey: string;
}
```

In `find`, replace the success return with `return { pngKey: `${base}.png`, ...provenance };`.

Replace `put` entirely:

```ts
  async put(req: PosterRequest, png: ArtifactRef, provenance: PosterProvenance): Promise<PosterArtifacts> {
    const base = posterKeyBase(req);
    await this.putFile(`${base}.png`, png);
    // The sidecar goes LAST. `find` keys off it, so its presence proves the png
    // is complete and a half-written poster is never served.
    await this.s3.send(
      new PutObjectCommand({
        Bucket: this.bucket,
        Key: `${base}.json`,
        Body: JSON.stringify(provenance),
        ContentType: "application/json",
      }),
    );
    return { pngKey: `${base}.png`, ...provenance };
  }
```

Update the `PosterSink` interface signature and make `StubPosterSink.put`/`find` match (both return `{ pngKey, ...provenance }`).

- [ ] **Step 4: Add the bounds and narrow the result**

In `src/poster-schema.ts`:

```ts
// Length bounds. These MUST match the Go handler and the poster_jobs CHECK
// constraints — if Go accepts what this rejects, the caller gets a 202 and then
// a silently failed job instead of a clean 400.
export const MAX_PERFORMER = 200;
export const MAX_VENUE = 200;
export const MAX_DATE = 100;

export const PosterRequestSchema = z
  .object({
    // .trim() runs before .max(), so the bound measures the trimmed value —
    // which is what JobID hashes.
    performer: z.string().trim().min(1, "performer is required").max(MAX_PERFORMER, `performer must be at most ${MAX_PERFORMER} characters`),
    venue: z.string().trim().min(1, "venue is required").max(MAX_VENUE, `venue must be at most ${MAX_VENUE} characters`),
    date: z.string().trim().min(1, "date is required").max(MAX_DATE, `date must be at most ${MAX_DATE} characters`),
    // Poster generation is LLM-driven and nondeterministic, so a user who
    // dislikes a result needs a re-roll. NOT part of posterKeyBase: a forced run
    // overwrites the same keys rather than creating a parallel copy.
    force: z.boolean().optional().default(false),
  })
  .strict();
```

And the result type:

```ts
export type PosterResult =
  | { ok: true; pngKey: string; cached: boolean; artist?: ArtistMatch; credit?: ImageCredit }
  | { ok: false; stage: "image" | "svg"; reason: string; artist?: ArtistMatch };
```

- [ ] **Step 5: Update the HTTP boundary and the local script**

In `src/poster.ts`, `processPosterRequest` passes one ref — `deps.sink.put(req, out.render.png, {...})` — and guards on `!out.render` as before. The 200 body becomes:

```ts
      body: JSON.stringify({
        pngKey: result.pngKey,
        cached: result.cached,
        artist: result.artist,
        credit: result.credit,
      }),
```

In `scripts/invoke-poster-local.ts`, drop the `poster.svg` copy and its log line; keep only `await copyFile(out.render.png.path, "poster.png");` and adjust the success message to `"wrote poster.png"`.

- [ ] **Step 6: Run tests**

Run: `pnpm test && pnpm typecheck`
Expected: all pass, typecheck exit 0. Commit.

---

### Task 3: Migration 0023 — drop `svg_key`, add CHECK constraints

**Files:**
- Create: `sql/migrations/0023_poster_jobs_svg_and_bounds.up.sql`, `sql/migrations/0023_poster_jobs_svg_and_bounds.down.sql`
- Modify: `sql/queries/poster_jobs.sql`, `internal/store/` (generated)

**Interfaces:**
- Produces: `MarkPosterJobReadyParams` without `SvgKey`; `PosterJob` without `SvgKey`. Task 4 consumes both.

- [ ] **Step 1: Write the migration**

`sql/migrations/0023_poster_jobs_svg_and_bounds.up.sql`:

```sql
-- The SVG artifact is gone: only the PNG is wanted, and dropping the artifact
-- also removes the stored-XSS surface it carried (it was served as
-- image/svg+xml after only an XML well-formedness check).
ALTER TABLE poster_jobs DROP COLUMN svg_key;

-- Length bounds. These MUST match the Go handler and the Lambda's zod schema.
-- The handlers reject over-long input at the edge, so nothing should ever reach
-- these — they exist for the second writer that does not exist yet.
ALTER TABLE poster_jobs
    ADD CONSTRAINT poster_jobs_performer_len CHECK (char_length(performer) <= 200),
    ADD CONSTRAINT poster_jobs_venue_len     CHECK (char_length(venue) <= 200),
    ADD CONSTRAINT poster_jobs_date_len      CHECK (char_length(date) <= 100);
```

`sql/migrations/0023_poster_jobs_svg_and_bounds.down.sql`:

```sql
ALTER TABLE poster_jobs
    DROP CONSTRAINT poster_jobs_performer_len,
    DROP CONSTRAINT poster_jobs_venue_len,
    DROP CONSTRAINT poster_jobs_date_len;

-- Nullable: rows written after the drop have no svg key to restore.
ALTER TABLE poster_jobs ADD COLUMN svg_key TEXT;
```

- [ ] **Step 2: Update the queries**

READ `sql/queries/poster_jobs.sql` FIRST. `MarkPosterJobReady` carries `user_id`
scoping added in an earlier fix wave; preserve that clause exactly as written.
The ONLY change is removing the `svg_key = $N,` assignment and renumbering the
positional parameters that followed it. The result should look like this, with
whatever `WHERE` clause the current statement actually has:

```sql
-- name: MarkPosterJobReady :exec
UPDATE poster_jobs
SET status = 'ready', png_key = $2, artist = $3, credit = $4,
    failure_stage = NULL, failure_reason = NULL, updated_at = NOW()
WHERE <preserve the existing WHERE clause, renumbering its params>;
```

In `ClaimPosterJob`'s `ON CONFLICT DO UPDATE SET` reset list, delete the
`svg_key = NULL,` line. Leave every other reset field alone — the list must
still clear everything a previous attempt could have set.

Report the final parameter order for `MarkPosterJobReadyParams` in your report;
Task 4 calls it.

- [ ] **Step 3: Generate and apply**

Run: `sqlc generate`
Then confirm both `internal/store/poster_jobs.sql.go` and `internal/store/models.go` changed, and that `PosterJob` no longer has `SvgKey`.

Run: `make migrate-test` to bring the test database to 23.

- [ ] **Step 4: Prove the CHECK constraints are real, not merely declared**

A constraint that was written but never enforced looks identical to one that
works. Add to `internal/store/poster_jobs_test.go` (create it if absent, using
the project's `internal/testdb` helper exactly as the handler tests do):

```go
func TestPosterJobs_RejectsOverlongFieldsAtTheDatabase(t *testing.T) {
	q := newTestQueries(t) // match the helper the other tests in this package use

	for _, tc := range []struct {
		name                    string
		performer, venue, date  string
	}{
		{"performer", strings.Repeat("a", 201), "V", "D"},
		{"venue", "P", strings.Repeat("a", 201), "D"},
		{"date", "P", "V", strings.Repeat("a", 101)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := q.ClaimPosterJob(context.Background(), store.ClaimPosterJobParams{
				ID: "chk-" + tc.name, UserID: testUserID(t, q),
				Performer: tc.performer, Venue: tc.venue, Date: tc.date,
				StaleBefore: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			})
			// The handlers reject this at the edge; the constraint is the backstop
			// for a writer that does not go through them.
			require.Error(t, err)
			require.Contains(t, err.Error(), "poster_jobs_"+tc.name+"_len")
		})
	}
}

func TestPosterJobs_AcceptsFieldsAtTheLimit(t *testing.T) {
	q := newTestQueries(t)
	_, err := q.ClaimPosterJob(context.Background(), store.ClaimPosterJobParams{
		ID: "chk-ok", UserID: testUserID(t, q),
		Performer: strings.Repeat("a", 200), Venue: strings.Repeat("b", 200),
		Date: strings.Repeat("c", 100),
		StaleBefore: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)
}
```

Read the neighbouring tests first and match how they obtain a `*store.Queries`
and a valid `user_id` — `poster_jobs.user_id` has a foreign key to `users`, so
the row needs a real user. Replace `newTestQueries`/`testUserID` with the actual
helpers.

- [ ] **Step 5: Verify**

Run: `go test ./internal/store/... && go build ./...`
Expected: the store tests PASS. `go build` FAILS in
`internal/http/handlers/posters.go` and `internal/poster/client.go`, which still
reference `SvgKey` — that is expected, Task 4 fixes them. Note the errors and
commit the migration, queries, the new store test, and both generated files.

---

### Task 4: Go — drop `SvgKey`/`svgUrl`, add length bounds and a body limit

**Files:**
- Modify: `internal/poster/client.go`, `internal/poster/client_test.go`, `internal/http/handlers/posters.go`, `internal/http/handlers/posters_test.go`

**Interfaces:**
- Consumes: `MarkPosterJobReadyParams` without `SvgKey` (Task 3); the Lambda's 200 body `{ pngKey, cached, artist?, credit? }` (Task 2).
- Produces: `poster.Result` without `SvgKey`; exported `poster.MaxPerformer = 200`, `MaxVenue = 200`, `MaxDate = 100`, `MaxRequestBody = 8 << 10`.

- [ ] **Step 1: Write the failing tests**

In `internal/http/handlers/posters_test.go` add:

```go
func TestCreatePoster_RejectsOverlongFields(t *testing.T) {
	h := newPosterHandlerForTest(t, &stubGenerator{})

	for _, tc := range []struct {
		name, body string
	}{
		{"performer", `{"performer":"` + strings.Repeat("a", 201) + `","venue":"V","date":"D"}`},
		{"venue", `{"performer":"P","venue":"` + strings.Repeat("a", 201) + `","date":"D"}`},
		{"date", `{"performer":"P","venue":"V","date":"` + strings.Repeat("a", 101) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/posters", strings.NewReader(tc.body)))
			require.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

func TestCreatePoster_AcceptsFieldsAtTheLimit(t *testing.T) {
	h := newPosterHandlerForTest(t, &stubGenerator{release: make(chan struct{})})
	body := `{"performer":"` + strings.Repeat("a", 200) + `","venue":"` + strings.Repeat("b", 200) +
		`","date":"` + strings.Repeat("c", 100) + `"}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/posters", strings.NewReader(body)))
	require.Equal(t, http.StatusAccepted, rec.Code)
}

func TestCreatePoster_RejectsAnOversizeBody(t *testing.T) {
	// MaxBytesReader must reject before the decoder allocates the whole thing.
	h := newPosterHandlerForTest(t, &stubGenerator{})
	huge := `{"performer":"` + strings.Repeat("a", 9000) + `","venue":"V","date":"D"}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/posters", strings.NewReader(huge)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetPoster_RejectsOverlongQueryValues(t *testing.T) {
	// Without this, a 1MB query string still reaches JobID.
	h := newPosterGetHandlerForTest(t)
	rec := httptest.NewRecorder()
	u := "/posters?performer=" + strings.Repeat("a", 201) + "&venue=V&date=D"
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, u, nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
```

Match the existing helper names in that file — read them first; the two used above are illustrative and must be replaced with whatever the file actually provides. Also change the existing ready-GET assertion to require `pngUrl` and assert `svgUrl` is absent from the decoded body.

In `internal/poster/client_test.go`, change the success fixture body to `{"pngKey":"posters/v1/a/b.png","cached":true}` and assert `res.PngKey`; drop every `SvgKey` reference.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/http/handlers/ -run Poster 2>&1 | head -20`
Expected: FAIL — the package does not compile (`SvgKey` undefined after Task 3) and the new bounds tests have no implementation.

- [ ] **Step 3: Narrow the client**

In `internal/poster/client.go`, drop `SvgKey` from `Result` and from the 200 decode struct so only `PngKey` remains. Add the limits beside the existing constants:

```go
// Request field bounds. These MUST match the Lambda's zod schema and the
// poster_jobs CHECK constraints. A mismatch means the caller gets a 202 and
// then a silently failed job instead of a clean 400.
const (
	MaxPerformer   = 200
	MaxVenue       = 200
	MaxDate        = 100
	MaxRequestBody = 8 << 10
)
```

- [ ] **Step 4: Enforce the bounds in the handler**

In `internal/http/handlers/posters.go`, add a shared validator and use it on both paths:

```go
// validateFields bounds the three natural-key fields. Measured AFTER trimming,
// because poster.JobID normalizes by trimming — bounding the raw string would
// let two inputs disagree on acceptance while producing the same job.
func validateFields(performer, venue, date string) (string, string, string, error) {
	performer, venue, date = strings.TrimSpace(performer), strings.TrimSpace(venue), strings.TrimSpace(date)
	switch {
	case performer == "" || venue == "" || date == "":
		return "", "", "", errors.New("performer, venue and date are required")
	case len(performer) > poster.MaxPerformer:
		return "", "", "", fmt.Errorf("performer must be at most %d characters", poster.MaxPerformer)
	case len(venue) > poster.MaxVenue:
		return "", "", "", fmt.Errorf("venue must be at most %d characters", poster.MaxVenue)
	case len(date) > poster.MaxDate:
		return "", "", "", fmt.Errorf("date must be at most %d characters", poster.MaxDate)
	}
	return performer, venue, date, nil
}
```

In `CreatePoster`, wrap the body before decoding and replace the existing emptiness check:

```go
		r.Body = http.MaxBytesReader(w, r.Body, poster.MaxRequestBody)
		var in posterRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			httperr.Write(w, http.StatusBadRequest, "invalid_body", "request body is not valid JSON or is too large")
			return
		}
		performer, venue, date, err := validateFields(in.Performer, in.Venue, in.Date)
		if err != nil {
			httperr.Write(w, http.StatusBadRequest, "invalid_body", err.Error())
			return
		}
```

Use the returned trimmed values for `poster.JobID` and for the claim/generate calls. Apply the same `validateFields` call in `GetPoster` after reading the query parameters, returning the same 400 shape.

- [ ] **Step 5: Drop the SVG from the ready path**

Still in `posters.go`: the loop that validates keys before `MarkPosterJobReady` now iterates only `res.PngKey`; `MarkPosterJobReadyParams` no longer takes `SvgKey`; and the ready branch of `GetPoster` presigns only `job.PngKey` and returns `"pngUrl"` with no `"svgUrl"`.

- [ ] **Step 6: Run everything**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all pass. Commit.

---

### Task 5: Docs and full verification

**Files:**
- Modify: `README.md`, `docs/superpowers/specs/2026-08-09-file-backed-poster-artifacts-design.md`, `docs/superpowers/specs/2026-08-10-poster-proxy-design.md`

- [ ] **Step 1: Update the README**

Replace the poster paragraph's contract with the live one: the request body is `{ performer, venue, date, force? }` with `performer`/`venue` at most 200 characters and `date` at most 100; the Lambda's internal endpoint returns `{ pngKey, cached, artist?, credit? }`; the API's `GET /posters` returns `{ status, pngUrl, artist?, credit? }` when ready. State plainly that no SVG is produced or served.

- [ ] **Step 2: Add superseding notes to the two prior specs**

At the top of each, directly under the `**Status:**` line, add — do NOT rewrite their bodies, they are the record of what was decided at the time:

```markdown
> **Superseded in part (2026-08-11):** the SVG artifact described below is no
> longer produced, stored, or served — see
> `docs/superpowers/specs/2026-08-11-drop-svg-artifact-design.md`. Everything
> about the PNG, the provenance sidecar, and presigning still applies.
```

- [ ] **Step 3: Full verification**

Run each and report the output:

```bash
go build ./... && go vet ./... && go test ./...
cd lambda/mastra-handler && pnpm test && pnpm typecheck && cd ../..
cd lambda/mastra-handler && pnpm test:integration && cd ../..
```

- [ ] **Step 4: Prove the removal, by grep**

```bash
grep -rn "svgKey\|svg_key\|SvgKey\|svgUrl\|authoredSvg" \
  --include="*.ts" --include="*.go" --include="*.sql" . | grep -v node_modules
```

Expected: **no matches in code**. Hits are permitted only in `docs/superpowers/plans/` and `docs/superpowers/specs/` — those are the historical record. The `0023` down migration mentions `svg_key` by necessity; that is correct and expected. Report anything else.

- [ ] **Step 5: Confirm the bounds agree across all three layers**

```bash
grep -n "MaxPerformer\|MaxVenue\|MaxDate" internal/poster/client.go
grep -n "MAX_PERFORMER\|MAX_VENUE\|MAX_DATE" lambda/mastra-handler/src/poster-schema.ts
grep -n "char_length" sql/migrations/0023_poster_jobs_svg_and_bounds.up.sql
```

Expected: 200 / 200 / 100 in all three. A mismatch here is the failure this whole arrangement exists to prevent — report it rather than fixing it silently.

- [ ] **Step 6: Commit**

---

## Notes for the implementer

**Removal must be asserted, not merely omitted.** A test that stops mentioning `svgKey` proves nothing. Task 1 checks the run directory contains no `.svg` file; Task 2 checks `put` issues exactly two `PutObjectCommand`s; Task 4 checks `svgUrl` is absent from the response body. Keep those assertions positive.

**The sidecar is load-bearing.** It stays, it is still written LAST, and the writes stay sequential — `find()` treats its presence as proof the PNG is complete. The `maxInFlight` assertion pins this and must keep passing.

**Trimming and bounding are coupled.** `poster.JobID` hashes the trimmed, lowercased values. Bounds measured on the untrimmed string would let `"a"` and `"a" + 199 spaces` disagree on acceptance while producing the same job id.

**Never edit `0022`.** It is applied to the local dev database, and `golang-migrate` stores only an integer version with no content checksum — editing it would leave that database silently holding the old schema while reporting itself current.

**Still out of scope after this:** rendering a CC BY-SA credit line onto the poster, and bounding any other user input in the codebase.
