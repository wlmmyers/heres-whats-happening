/* Republish existing prod events onto the events-queue so the enrichment Lambda
 * processes them the way it processes new traffic.
 *
 * Stage 2 of the backfill. Stage 1 is backfill-enrichment.sql, which produces
 * the JSONL this reads.
 *
 *   # dry run (default) — validates everything, sends nothing
 *   pnpm backfill-enrichment /tmp/backfill.jsonl
 *
 *   # canary, then the rest
 *   AWS_PROFILE=servant AWS_REGION=us-west-2 EVENTS_QUEUE_URL=... \
 *     pnpm backfill-enrichment /tmp/backfill.jsonl --publish --limit 5
 *   AWS_PROFILE=servant AWS_REGION=us-west-2 EVENTS_QUEUE_URL=... \
 *     pnpm backfill-enrichment /tmp/backfill.jsonl --publish
 *
 * Resumable: every published event is appended to a progress file and skipped
 * on re-run, so an interrupted run does not re-pay for enrichment.
 */
import { appendFileSync, existsSync, readFileSync } from 'node:fs';
import { SQSClient } from '@aws-sdk/client-sqs';
import { sendBatch } from '../src/sqs.js';
import { buildMessage, pickHeadliner, type EventRow, type HeadlinerSignal } from './backfill.js';

const MAX_BATCH = 10; // mirrors sendBatch's SQS limit, so pacing lines up with sends

/** Queue settings the estimate below reasons about. Mirrored by hand from
 * terraform/prod/sqs.tf and terraform/prod/enrichment.tf — if those change and
 * this does not, the only casualty is the accuracy of a printed warning. */
const QUEUE_RETENTION_SECONDS = 345_600; // 4 days
const LAMBDA_CONCURRENCY = 2;
/** Rough seconds per message. Cold artists run three workflows in parallel;
 * cache hits return in about a second. Used only for the drain estimate. */
const EST_SECONDS_PER_MESSAGE = 20;

interface Args {
  file: string;
  publish: boolean;
  limit: number;
  ratePerSecond: number;
  progressFile: string;
}

function parseArgs(argv: string[]): Args {
  const positional: string[] = [];
  const args: Args = {
    file: '',
    publish: false,
    limit: Infinity,
    ratePerSecond: 10,
    progressFile: '',
  };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i]!;
    switch (a) {
      case '--publish':
        args.publish = true;
        break;
      case '--limit':
        args.limit = Number(argv[++i]);
        break;
      case '--rate':
        args.ratePerSecond = Number(argv[++i]);
        break;
      case '--progress':
        args.progressFile = argv[++i] ?? '';
        break;
      default:
        if (a.startsWith('--')) throw new Error(`unknown flag ${a}`);
        positional.push(a);
    }
  }
  if (positional.length !== 1) {
    throw new Error(
      'usage: backfill-enrichment <rows.jsonl> [--publish] [--limit N] [--rate N] [--progress FILE]',
    );
  }
  args.file = positional[0]!;
  if (!Number.isFinite(args.limit) && args.limit !== Infinity) {
    throw new Error('--limit must be a number');
  }
  if (!Number.isFinite(args.ratePerSecond) || args.ratePerSecond <= 0) {
    throw new Error('--rate must be a positive number');
  }
  if (!args.progressFile) args.progressFile = `${args.file}.sent`;
  return args;
}

/** Stable identity of an event — the same pair events is UNIQUE on. */
function key(row: Pick<EventRow, 'source_name' | 'source_event_id'>): string {
  return `${row.source_name}\t${row.source_event_id}`;
}

function readRows(file: string): EventRow[] {
  const text = readFileSync(file, 'utf8');
  const rows: EventRow[] = [];
  const lines = text.split('\n');
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]!.trim();
    if (line === '') continue;
    try {
      rows.push(JSON.parse(line) as EventRow);
    } catch (e) {
      throw new Error(`${file}:${i + 1}: not JSON: ${e instanceof Error ? e.message : String(e)}`, {
        cause: e,
      });
    }
  }
  return rows;
}

function readProgress(file: string): Set<string> {
  if (!existsSync(file)) return new Set();
  return new Set(
    readFileSync(file, 'utf8')
      .split('\n')
      .map((l) => l.trim())
      .filter((l) => l !== ''),
  );
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function humanDuration(seconds: number): string {
  if (seconds < 60) return `${Math.ceil(seconds)}s`;
  if (seconds < 3600) return `${(seconds / 60).toFixed(1)} min`;
  if (seconds < 86_400) return `${(seconds / 3600).toFixed(1)} h`;
  return `${(seconds / 86_400).toFixed(1)} days`;
}

const args = parseArgs(process.argv.slice(2));
const rows = readRows(args.file);
const alreadySent = readProgress(args.progressFile);

// Build and validate EVERYTHING before sending anything. A schema failure on
// row 900 must not leave 899 events already on the queue.
const pending: {
  row: EventRow;
  message: ReturnType<typeof buildMessage>;
  signal: HeadlinerSignal;
}[] = [];
const skipped: EventRow[] = [];
const failures: string[] = [];

for (const row of rows) {
  if (alreadySent.has(key(row))) {
    skipped.push(row);
    continue;
  }
  try {
    pending.push({ row, message: buildMessage(row), signal: pickHeadliner(row).signal });
  } catch (e) {
    failures.push(`${key(row).replace('\t', '/')}: ${e instanceof Error ? e.message : String(e)}`);
  }
}

if (failures.length > 0) {
  console.error(`\n${failures.length} row(s) failed to build a valid message:\n`);
  for (const f of failures.slice(0, 20)) console.error(`  ${f}`);
  if (failures.length > 20) console.error(`  ... and ${failures.length - 20} more`);
  console.error('\nNothing was published. Fix the extract and re-run.');
  process.exit(1);
}

const selected = pending.slice(0, args.limit);

// Cost tracks DISTINCT artists, not events: the S3 enrichment cache is keyed by
// performer, so a band playing five dates is enriched once.
const distinctHeadliners = new Set(selected.map((p) => p.message.performers![0]!));
const bySignal = selected.reduce<Record<string, number>>((acc, p) => {
  acc[p.signal] = (acc[p.signal] ?? 0) + 1;
  return acc;
}, {});

console.log(`\nrows in file        ${rows.length}`);
console.log(`already published   ${skipped.length}`);
console.log(
  `to publish          ${selected.length}${
    selected.length < pending.length ? ` (of ${pending.length}, --limit ${args.limit})` : ''
  }`,
);
console.log(`distinct headliners ${distinctHeadliners.size}   <- what enrichment actually costs`);
console.log(`\nheadliner signal:`);
for (const [signal, n] of Object.entries(bySignal).sort((a, b) => b[1] - a[1])) {
  console.log(`  ${signal.padEnd(16)} ${n}`);
}

const drainSeconds = (selected.length * EST_SECONDS_PER_MESSAGE) / LAMBDA_CONCURRENCY;
console.log(
  `\nestimated drain     ~${humanDuration(drainSeconds)} at concurrency ${LAMBDA_CONCURRENCY}` +
    ` (~${EST_SECONDS_PER_MESSAGE}s/message)`,
);
if (drainSeconds > QUEUE_RETENTION_SECONDS) {
  console.log(
    `\n!! That exceeds the queue's ${humanDuration(QUEUE_RETENTION_SECONDS)} retention.` +
      ` Messages could expire before the Lambda reaches them.` +
      `\n   Publish in chunks with --limit instead.`,
  );
}

// The title heuristic is the one guess in the pipeline, and multi-performer
// events are where it can be wrong. Show them so they can be eyeballed.
const titlePicks = selected.filter((p) => p.signal === 'title' && p.row.performers.length > 1);
if (titlePicks.length > 0) {
  console.log(`\nheadliner guessed from title (${titlePicks.length} multi-artist events):`);
  for (const p of titlePicks.slice(0, 20)) {
    console.log(`  ${JSON.stringify(p.row.title)}`);
    console.log(`    -> ${p.message.performers!.join(' | ')}`);
  }
  if (titlePicks.length > 20) console.log(`  ... and ${titlePicks.length - 20} more`);
}

if (selected[0]) {
  console.log(`\nsample message:\n${JSON.stringify(selected[0].message, null, 2)}`);
}

if (!args.publish) {
  console.log(`\nDRY RUN — nothing published. Re-run with --publish to send.\n`);
  process.exit(0);
}

if (selected.length === 0) {
  console.log(`\nNothing to publish.\n`);
  process.exit(0);
}

const region = process.env.AWS_REGION;
if (!region) throw new Error('set AWS_REGION');
const queueUrl = process.env.EVENTS_QUEUE_URL;
if (!queueUrl) throw new Error('set EVENTS_QUEUE_URL');

const sqs = new SQSClient({ region, endpoint: process.env.SQS_ENDPOINT || undefined });
const batchIntervalMs = (MAX_BATCH / args.ratePerSecond) * 1000;

console.log(`\npublishing ${selected.length} message(s) to ${queueUrl}`);
console.log(`progress -> ${args.progressFile}\n`);

let sent = 0;
for (let i = 0; i < selected.length; i += MAX_BATCH) {
  const chunk = selected.slice(i, i + MAX_BATCH);
  // Throws on any failed entry, which aborts the run. The progress file already
  // holds everything sent so far, so a re-run picks up from here.
  await sendBatch(
    sqs,
    queueUrl,
    chunk.map((p) => p.message),
  );
  // Written only after the send succeeded — a crash mid-run can duplicate a
  // batch on resume, never silently skip one. Duplicates are harmless:
  // source_event_id is stable and the consumer upserts.
  appendFileSync(args.progressFile, chunk.map((p) => key(p.row)).join('\n') + '\n');
  sent += chunk.length;
  process.stdout.write(`\r  ${sent}/${selected.length}`);
  if (i + MAX_BATCH < selected.length) await sleep(batchIntervalMs);
}

console.log(`\n\npublished ${sent} message(s). Watch the drain with:`);
console.log(
  `  aws sqs get-queue-attributes --queue-url ${queueUrl} \\\n` +
    `    --attribute-names ApproximateNumberOfMessages ApproximateNumberOfMessagesNotVisible\n`,
);
