import { describe, expect, it } from 'vitest';
import { stubFetch, type StubRoute } from './stub-fetch.js';
import { createWikimediaClient } from './wikimedia.tool.js';

const MBID = '9b5ae4cc-15ae-4f0b-8a4e-8c44e42ba52a';
const QID = 'Q21485176';
const P18_FILE = 'La Luz performing at Siberia in New Orleans, LA 5 March 2015.jpg';

const searchHit = { query: { search: [{ title: QID }] } };
const searchEmpty = { query: { search: [] } };
const claimsP18 = { claims: { P18: [{ mainsnak: { datavalue: { value: P18_FILE } } }] } };
const claimsP373 = { claims: { P373: [{ mainsnak: { datavalue: { value: 'La Luz (band)' } } }] } };
const claimsNone = { claims: {} };

function categoryOf(...titles: string[]) {
  return {
    query: { categorymembers: titles.map((t, i) => ({ pageid: 100 + i, title: `File:${t}` })) },
  };
}

function imageinfoPage(
  title: string,
  extra: Record<string, unknown> = {},
  meta: Record<string, string> = {},
) {
  return {
    title: `File:${title}`,
    imageinfo: [
      {
        mime: 'image/jpeg',
        width: 1600,
        height: 1200,
        thumbwidth: 1080,
        thumbheight: 810,
        thumburl: `https://upload.wikimedia.org/thumb/${encodeURIComponent(title)}`,
        descriptionurl: `https://commons.wikimedia.org/wiki/File:${title.replace(/ /g, '_')}`,
        extmetadata: Object.fromEntries(Object.entries(meta).map(([k, v]) => [k, { value: v }])),
        ...extra,
      },
    ],
  };
}

/**
 * `pages` is keyed by pageid. JS iterates integer-like keys in ascending
 * numeric order, so iteration follows ARGUMENT order here. Callers that want to
 * prove title-joining (rather than positional joining) pass pages in a
 * different order from the titles they requested.
 */
function imageinfoOf(...pages: ReturnType<typeof imageinfoPage>[]) {
  return { query: { pages: Object.fromEntries(pages.map((p, i) => [String(900 + i), p])) } };
}

const ROUTE_SEARCH: StubRoute = { match: /haswbstatement/, json: searchHit };

function client(routes: StubRoute[]) {
  const f = stubFetch(routes);
  const c = createWikimediaClient({
    wikidataBaseUrl: 'https://wd.test',
    commonsBaseUrl: 'https://commons.test',
    fetchFn: f,
  });
  return { c, f };
}

describe('resolveImageCandidates', () => {
  it('returns the P18 image first, tagged as p18', async () => {
    const { c } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsP18 },
      { match: /property=P373/, json: claimsNone },
      { match: /prop=imageinfo/, json: imageinfoOf(imageinfoPage(P18_FILE)) },
    ]);
    const out = await c.resolveImageCandidates(MBID);
    expect(out).toHaveLength(1);
    expect(out[0].source).toBe('p18');
    expect(out[0].file).toBe(`File:${P18_FILE}`);
  });

  it('uses THUMBNAIL dimensions, not the original', async () => {
    const { c } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsP18 },
      { match: /property=P373/, json: claimsNone },
      { match: /prop=imageinfo/, json: imageinfoOf(imageinfoPage(P18_FILE)) },
    ]);
    const [only] = await c.resolveImageCandidates(MBID);
    expect(only.width).toBe(1080);
    expect(only.height).toBe(810);
    expect(only.url).toContain('upload.wikimedia.org/thumb');
  });

  it('requests thumbnails at the poster canvas width', async () => {
    const { c, f } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsP18 },
      { match: /property=P373/, json: claimsNone },
      { match: /prop=imageinfo/, json: imageinfoOf(imageinfoPage(P18_FILE)) },
    ]);
    await c.resolveImageCandidates(MBID);
    const call = f.calls.find((x) => x.url.includes('prop=imageinfo'))!;
    expect(new URL(call.url).searchParams.get('iiurlwidth')).toBe('1080');
  });

  it('returns [] when the MBID has no Wikidata entity', async () => {
    const { c } = client([{ match: /haswbstatement/, json: searchEmpty }]);
    expect(await c.resolveImageCandidates(MBID)).toEqual([]);
  });

  it('returns [] when the entity has neither P18 nor P373', async () => {
    const { c } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsNone },
      { match: /property=P373/, json: claimsNone },
    ]);
    expect(await c.resolveImageCandidates(MBID)).toEqual([]);
  });

  it('dedupes the P18 file out of the category listing', async () => {
    const { c } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsP18 },
      { match: /property=P373/, json: claimsP373 },
      // The P18 file really does appear in the category verbatim.
      { match: /list=categorymembers/, json: categoryOf(P18_FILE, 'Shana Cleveland.jpg') },
      {
        match: /prop=imageinfo/,
        json: imageinfoOf(imageinfoPage(P18_FILE), imageinfoPage('Shana Cleveland.jpg')),
      },
    ]);
    const out = await c.resolveImageCandidates(MBID);
    expect(out.map((x) => x.file)).toEqual([`File:${P18_FILE}`, 'File:Shana Cleveland.jpg']);
  });

  it('orders category files containing the artist name ahead of the rest', async () => {
    const { c } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsNone },
      { match: /property=P373/, json: claimsP373 },
      {
        match: /list=categorymembers/,
        json: categoryOf(
          'Jenn Ghetto, Lena Simon & Shana Cleveland - Pop Conference 2015 - 01.jpg',
          'La Luz performing at Siberia in New Orleans, LA 16 August 2015.jpg',
          'Shana Cleveland.jpg',
        ),
      },
      {
        match: /prop=imageinfo/,
        json: imageinfoOf(
          imageinfoPage('Jenn Ghetto, Lena Simon & Shana Cleveland - Pop Conference 2015 - 01.jpg'),
          imageinfoPage('La Luz performing at Siberia in New Orleans, LA 16 August 2015.jpg'),
          imageinfoPage('Shana Cleveland.jpg'),
        ),
      },
    ]);
    const out = await c.resolveImageCandidates(MBID, { artistName: 'La Luz' });
    // Alphabetically "Jenn Ghetto..." would win; the artist-name rule promotes the band photo.
    expect(out[0].file).toContain('La Luz performing');
  });

  it('joins imageinfo by title even when pages come back shuffled', async () => {
    const { c } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsNone },
      { match: /property=P373/, json: claimsP373 },
      { match: /list=categorymembers/, json: categoryOf('A.jpg', 'B.jpg') },
      {
        // Titles were requested A,B — pages come back B,A. Positional joining
        // would swap the dimensions; joining by title must not.
        match: /prop=imageinfo/,
        json: imageinfoOf(
          imageinfoPage('B.jpg', { thumbwidth: 800, thumbheight: 600 }),
          imageinfoPage('A.jpg', { thumbwidth: 640, thumbheight: 480 }),
        ),
      },
    ]);
    const out = await c.resolveImageCandidates(MBID, { artistName: 'Nobody' });
    const a = out.find((x) => x.file === 'File:A.jpg')!;
    const b = out.find((x) => x.file === 'File:B.jpg')!;
    expect(a.width).toBe(640);
    expect(b.width).toBe(800);
  });

  it('drops non-image files that Commons categories carry', async () => {
    const { c } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsNone },
      { match: /property=P373/, json: claimsP373 },
      { match: /list=categorymembers/, json: categoryOf('Photo.jpg', 'Doc.pdf', 'Clip.ogv') },
      {
        match: /prop=imageinfo/,
        json: imageinfoOf(
          imageinfoPage('Photo.jpg'),
          imageinfoPage('Doc.pdf', { mime: 'application/pdf' }),
          imageinfoPage('Clip.ogv', { mime: 'video/ogg' }),
        ),
      },
    ]);
    const out = await c.resolveImageCandidates(MBID, { artistName: 'zzz' });
    expect(out.map((x) => x.file)).toEqual(['File:Photo.jpg']);
  });

  it('caps the pool at 12 candidates', async () => {
    const titles = Array.from({ length: 20 }, (_, i) => `Pic ${String(i).padStart(2, '0')}.jpg`);
    const { c } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsNone },
      { match: /property=P373/, json: claimsP373 },
      { match: /list=categorymembers/, json: categoryOf(...titles) },
      { match: /prop=imageinfo/, json: imageinfoOf(...titles.map((t) => imageinfoPage(t))) },
    ]);
    expect(await c.resolveImageCandidates(MBID, { artistName: 'zzz' })).toHaveLength(12);
  });

  it('sends the User-Agent on every Wikimedia call', async () => {
    const { c, f } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsP18 },
      { match: /property=P373/, json: claimsNone },
      { match: /prop=imageinfo/, json: imageinfoOf(imageinfoPage(P18_FILE)) },
    ]);
    await c.resolveImageCandidates(MBID);
    expect(f.calls.length).toBeGreaterThan(0);
    for (const call of f.calls) {
      expect(call.headers['user-agent']).toBe('heres-whats-happening/1.0 ( wlmmyers@gmail.com )');
    }
  });
});

describe('licensing capture', () => {
  const meta = {
    License: 'cc-by-sa-4.0',
    LicenseShortName: 'CC BY-SA 4.0',
    LicenseUrl: 'https://creativecommons.org/licenses/by-sa/4.0',
    UsageTerms: 'Creative Commons Attribution-Share Alike 4.0',
    Artist: 'Shark2000br',
    Credit: 'Own work',
    AttributionRequired: 'true',
  };

  it('maps the extmetadata fields onto the credit', async () => {
    const { c } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsP18 },
      { match: /property=P373/, json: claimsNone },
      { match: /prop=imageinfo/, json: imageinfoOf(imageinfoPage(P18_FILE, {}, meta)) },
    ]);
    const [only] = await c.resolveImageCandidates(MBID);
    expect(only.credit.license).toBe('cc-by-sa-4.0');
    expect(only.credit.licenseShortName).toBe('CC BY-SA 4.0');
    expect(only.credit.artist).toBe('Shark2000br');
    expect(only.credit.attributionRequired).toBe(true);
    expect(only.credit.descriptionUrl).toContain('commons.wikimedia.org/wiki/File:');
  });

  it('requests extmetadata so licensing costs no extra call', async () => {
    const { c, f } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsP18 },
      { match: /property=P373/, json: claimsNone },
      { match: /prop=imageinfo/, json: imageinfoOf(imageinfoPage(P18_FILE, {}, meta)) },
    ]);
    await c.resolveImageCandidates(MBID);
    const call = f.calls.find((x) => x.url.includes('prop=imageinfo'))!;
    const iiprop = new URL(call.url).searchParams.get('iiprop')!;
    expect(iiprop).toContain('extmetadata');
    expect(iiprop).toContain('url');
  });

  it('strips the HTML Commons wraps around Credit', async () => {
    const html =
      '<a rel="nofollow" class="external free" href="https://www.flickr.com/photos/jmabel/16609507803/">https://www.flickr.com/photos/jmabel/16609507803/</a>';
    const { c } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsP18 },
      { match: /property=P373/, json: claimsNone },
      {
        match: /prop=imageinfo/,
        json: imageinfoOf(
          imageinfoPage(P18_FILE, {}, { Credit: html, Artist: '<span>Joe Mabel</span>' }),
        ),
      },
    ]);
    const [only] = await c.resolveImageCandidates(MBID);
    expect(only.credit.credit).toBe('https://www.flickr.com/photos/jmabel/16609507803/');
    expect(only.credit.artist).toBe('Joe Mabel');
  });

  it('keeps a public-domain file that has no Artist or License', async () => {
    const { c } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsP18 },
      { match: /property=P373/, json: claimsNone },
      { match: /prop=imageinfo/, json: imageinfoOf(imageinfoPage(P18_FILE, {}, {})) },
    ]);
    const [only] = await c.resolveImageCandidates(MBID);
    expect(only.credit.artist).toBeUndefined();
    expect(only.credit.license).toBeUndefined();
    expect(only.credit.attributionRequired).toBe(false);
  });
});

describe('fetchImageBytes', () => {
  const candidate = {
    file: `File:${P18_FILE}`,
    url: 'https://upload.wikimedia.org/thumb/la-luz.jpg',
    width: 1080,
    height: 810,
    contentType: 'image/jpeg',
    source: 'p18' as const,
    credit: {
      file: `File:${P18_FILE}`,
      descriptionUrl: 'https://commons.wikimedia.org/wiki/File:La_Luz.jpg',
      artist: 'Shark2000br',
      attributionRequired: true,
    },
  };

  it('returns the raw bytes so the caller decides where they land', async () => {
    const bytes = Buffer.from([0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10]);
    const { c } = client([{ match: /upload\.wikimedia/, body: bytes }]);
    expect(await c.fetchImageBytes(candidate)).toEqual(bytes);
  });

  it('throws on a non-2xx so the caller can count it as a failed attempt', async () => {
    const { c } = client([{ match: /upload\.wikimedia/, status: 404, json: {} }]);
    await expect(c.fetchImageBytes(candidate)).rejects.toThrow(/404/);
  });
});

describe('request efficiency', () => {
  it('requests the P18 title only ONCE when it is also a category member', async () => {
    const { c, f } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsP18 },
      { match: /property=P373/, json: claimsP373 },
      { match: /list=categorymembers/, json: categoryOf(P18_FILE, 'Other.jpg') },
      {
        match: /prop=imageinfo/,
        json: imageinfoOf(imageinfoPage(P18_FILE), imageinfoPage('Other.jpg')),
      },
    ]);
    await c.resolveImageCandidates(MBID);

    const calls = f.calls.filter((x) => x.url.includes('prop=imageinfo'));
    expect(calls).toHaveLength(1); // one batch, not spilled into two
    const titles = new URL(calls[0].url).searchParams.get('titles')!.split('|');
    expect(titles).toHaveLength(2);
    expect(new Set(titles).size).toBe(2); // no duplicate
  });

  it('caps the category request at 25 to keep the imageinfo url well under the GET limit', async () => {
    const { c, f } = client([
      ROUTE_SEARCH,
      { match: /property=P18/, json: claimsNone },
      { match: /property=P373/, json: claimsP373 },
      { match: /list=categorymembers/, json: categoryOf('A.jpg') },
      { match: /prop=imageinfo/, json: imageinfoOf(imageinfoPage('A.jpg')) },
    ]);
    await c.resolveImageCandidates(MBID);
    const cat = f.calls.find((x) => x.url.includes('list=categorymembers'))!;
    expect(new URL(cat.url).searchParams.get('cmlimit')).toBe('25');
  });
});
