/* Run the poster workflow locally and write the SVG + PNG to disk.
 * Usage: ANTHROPIC_API_KEY=... pnpm tsx scripts/invoke-poster-local.ts "Khruangbin" "The Fillmore" "2026-08-15"
 */
import { writeFileSync } from "node:fs";
import { runPosterWorkflow } from "../src/handler.js";

const [performer, venue, date] = process.argv.slice(2);
if (!performer || !venue || !date) throw new Error('usage: invoke-poster-local "<performer>" "<venue>" "<date>"');

const out = await runPosterWorkflow({ performer, venue, date });
if (!out.ok || !out.svg || !out.pngBase64) {
  console.error(JSON.stringify({ ok: false, failureStage: out.failureStage, reason: out.reason }, null, 2));
  process.exit(1);
}
writeFileSync("poster.svg", out.svg);
writeFileSync("poster.png", Buffer.from(out.pngBase64, "base64"));
console.log("wrote poster.svg + poster.png");
