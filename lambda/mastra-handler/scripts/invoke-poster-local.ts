/* Run the poster workflow locally and write the PNG to disk.
 * Usage: ANTHROPIC_API_KEY=... pnpm tsx scripts/invoke-poster-local.ts "Khruangbin" "The Fillmore" "2026-08-15"
 */
import { copyFile } from 'node:fs/promises';
import { stubAwsLambdaGlobal } from '../src/awslambda-stub.js';

const [performer, venue, date] = process.argv.slice(2);
if (!performer || !venue || !date)
  throw new Error('usage: invoke-poster-local "<performer>" "<venue>" "<date>"');

// handler.ts calls awslambda.streamifyResponse() at module load, so the global has
// to exist first. Static imports are hoisted, hence the dynamic import below.
stubAwsLambdaGlobal();
const { runPosterWorkflow } = await import('../src/handler.js');

// `force` skips the S3 lookup, which this script never reaches anyway — it calls
// the workflow directly rather than going through processPosterRequest. `userId`
// scopes the S3 key for the same reason, so a fixed local value is fine: nothing
// here writes to S3.
const LOCAL_USER_ID = '00000000-0000-4000-8000-000000000000';
const out = await runPosterWorkflow({
  userId: LOCAL_USER_ID,
  performer,
  venue,
  date,
  force: false,
});

if (!out.ok || !out.render) {
  console.error(
    JSON.stringify(
      { ok: false, failureStage: out.failureStage, reason: out.reason, artist: out.artist },
      null,
      2,
    ),
  );
  if (out.artifactDir) console.error(`\nartifacts kept for inspection: ${out.artifactDir}`);
  process.exit(1);
}

// The workflow writes into a run-scoped temp dir and returns a reference, so copy
// the finished artifact somewhere durable. The run dir is deliberately left in
// place — its intermediates (band-N.jpg) are the point of running this locally
// — and artifactStore's one-hour sweep reclaims it.
await copyFile(out.render.png.path, 'poster.png');

console.log('wrote poster.png');
console.log(`run artifacts: ${out.artifactDir}`);

if (out.artist) {
  const a = out.artist;
  console.log(`artist:  ${a.name}${a.disambiguation ? ` (${a.disambiguation})` : ''} [${a.mbid}]`);
}
if (out.credit) {
  const c = out.credit;
  console.log(`photo:   ${c.file}`);
  console.log(
    `credit:  ${c.artist ?? 'unknown'} — ${c.licenseShortName ?? 'unknown licence'}${c.attributionRequired ? ' (ATTRIBUTION REQUIRED)' : ''}`,
  );
  console.log(`source:  ${c.descriptionUrl}`);
}
