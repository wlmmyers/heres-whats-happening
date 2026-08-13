import { createHash } from 'node:crypto';
import { describe, expect, it } from 'vitest';
import { POSTER_FONT_FAMILY, rasterizeSvg } from './rasterize.tool.js';
import { STUB_BAND_IMAGE_BASE64 } from './stub-band-image.js';

const PNG_MAGIC = '89504e47';

describe('rasterizeSvg', () => {
  it('renders a plain SVG to a PNG', async () => {
    const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32"><rect width="32" height="32" fill="#0af"/></svg>`;
    const r = await rasterizeSvg(svg);
    expect(r.ok).toBe(true);
    expect(r.png).toBeInstanceOf(Buffer);
    // PNG signature: 89 50 4E 47
    expect(r.png!.subarray(0, 4).toString('hex')).toBe(PNG_MAGIC);
    expect(r.width).toBeGreaterThan(0);
  });

  it('renders an SVG with an embedded base64 JPEG <image>', async () => {
    const dataUri = `data:image/jpeg;base64,${STUB_BAND_IMAGE_BASE64}`;
    const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="48" height="48"><image x="0" y="0" width="48" height="48" href="${dataUri}"/></svg>`;
    const r = await rasterizeSvg(svg);
    expect(r.ok).toBe(true);
    expect(r.png).toBeInstanceOf(Buffer);
    // PNG signature: 89 50 4E 47
    expect(r.png!.subarray(0, 4).toString('hex')).toBe(PNG_MAGIC);
  });

  it('returns ok:false with an error for unrenderable input', async () => {
    const r = await rasterizeSvg('not an svg at all');
    expect(r.ok).toBe(false);
    expect(r.error).toBeTruthy();
  });
});

/**
 * The renderer ships no font database, so before fonts were supplied EVERY <text>
 * element drew nothing: a poster with a headline produced a PNG byte-identical to
 * the same poster with the headline deleted. Only a byte comparison catches that —
 * a test that renders a <rect> or an <image> passes either way, which is exactly
 * why it shipped. Compare renders, not just PNG magic bytes.
 */
describe('rasterizeSvg text rendering', () => {
  const digest = (b: Buffer) => createHash('sha256').update(b).digest('hex');
  const page = (inner: string) =>
    `<svg xmlns="http://www.w3.org/2000/svg" width="360" height="140"><rect width="360" height="140" fill="#fff"/>${inner}</svg>`;
  const headline = (family: string) =>
    `<text x="16" y="90" font-size="48" font-weight="900" font-family="${family}" fill="#111">KHRUANGBIN</text>`;

  it('draws <text> — the PNG differs from the same SVG with the text removed', async () => {
    const withText = await rasterizeSvg(page(headline(POSTER_FONT_FAMILY)));
    const without = await rasterizeSvg(page(''));

    expect(withText.ok).toBe(true);
    expect(without.ok).toBe(true);
    expect(withText.png!.equals(without.png!)).toBe(false);
    expect(digest(withText.png!)).not.toBe(digest(without.png!));
  });

  it('renders different strings to different pixels (the glyphs are real, not a blank box)', async () => {
    const a = await rasterizeSvg(page(headline(POSTER_FONT_FAMILY)));
    const b = await rasterizeSvg(
      page(
        `<text x="16" y="90" font-size="48" font-weight="900" font-family="${POSTER_FONT_FAMILY}" fill="#111">SIGUR ROS</text>`,
      ),
    );
    expect(digest(a.png!)).not.toBe(digest(b.png!));
  });

  it('falls back to the bundled family when the author names an unavailable font', async () => {
    // defaultFontFamily points at the one loaded family, so a stray "Helvetica"
    // still produces glyphs rather than silently vanishing.
    const stray = await rasterizeSvg(page(headline('Helvetica')));
    const blank = await rasterizeSvg(page(''));
    expect(stray.png!.equals(blank.png!)).toBe(false);
  });
});
