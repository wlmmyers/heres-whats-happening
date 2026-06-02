import { readFileSync } from "fs";
import { parseEmail } from "../src/email.js";

const file = process.argv[2];
if (!file) {
  console.error("Usage: parse-email <path-to-ses-email>");
  process.exit(1);
}

const raw = readFileSync(file);
const result = await parseEmail(raw);

const output = {
  ...result,
  images: result.images.map((img) => ({
    contentType: img.contentType,
    sizeBytes: img.data.byteLength,
  })),
};

console.log(JSON.stringify(output, null, 2));
