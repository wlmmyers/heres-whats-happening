import { describe, expect, it } from 'vitest';
import { stubFetch } from './stub-fetch.js';
import {
  MAX_EXTRACT_CHARS,
  fetchReleaseGroups,
  fetchWikipediaExtract,
} from './artist-facts.tool.js';

describe('fetchWikipediaExtract', () => {
  it('resolves the enwiki sitelink then fetches the extract and revision id', async () => {
    const fetchFn = stubFetch([
      {
        match: /wbgetentities/,
        json: { entities: { Q123: { sitelinks: { enwiki: { title: 'La Luz (band)' } } } } },
      },
      {
        match: /en\.wikipedia\.org/,
        json: {
          query: {
            pages: {
              '42': {
                pageid: 42,
                title: 'La Luz (band)',
                extract: 'La Luz is an American surf rock band formed in Seattle in 2012.',
                revisions: [{ revid: 987654 }],
              },
            },
          },
        },
      },
    ]);

    const got = await fetchWikipediaExtract('Q123', { fetchFn });
    expect(got?.title).toBe('La Luz (band)');
    expect(got?.revisionId).toBe(987654);
    expect(got?.text).toContain('surf rock');
    expect(got?.url).toBe('https://en.wikipedia.org/wiki/La_Luz_(band)');
  });

  it('returns null when the entity has no enwiki sitelink', async () => {
    const fetchFn = stubFetch([
      { match: /wbgetentities/, json: { entities: { Q123: { sitelinks: {} } } } },
    ]);
    expect(await fetchWikipediaExtract('Q123', { fetchFn })).toBeNull();
  });

  it('truncates a very long extract', async () => {
    const long = 'x'.repeat(MAX_EXTRACT_CHARS * 2);
    const fetchFn = stubFetch([
      {
        match: /wbgetentities/,
        json: { entities: { Q1: { sitelinks: { enwiki: { title: 'T' } } } } },
      },
      {
        match: /en\.wikipedia\.org/,
        json: {
          query: { pages: { '1': { title: 'T', extract: long, revisions: [{ revid: 1 }] } } },
        },
      },
    ]);
    const got = await fetchWikipediaExtract('Q1', { fetchFn });
    expect(got!.text.length).toBeLessThanOrEqual(MAX_EXTRACT_CHARS);
  });
});

describe('fetchReleaseGroups', () => {
  // Album titles and years are the part of a generated bio most likely to be
  // confidently wrong, so they come from structured data rather than from a
  // model reading prose.
  it('returns albums with their first-release years, newest first', async () => {
    const fetchFn = stubFetch([
      {
        match: /release-groups/,
        json: {
          'release-groups': [
            { title: "It's Alive", 'primary-type': 'Album', 'first-release-date': '2013-10-15' },
            {
              title: 'Floating Features',
              'primary-type': 'Album',
              'first-release-date': '2018-05-11',
            },
            {
              title: 'Sure As Spring',
              'primary-type': 'Single',
              'first-release-date': '2021-01-01',
            },
            { title: 'Untitled', 'primary-type': 'Album' },
          ],
        },
      },
    ]);

    const got = await fetchReleaseGroups('mbid-1', { fetchFn, minIntervalMs: 0 });
    expect(got).toEqual([
      { title: 'Floating Features', year: '2018' },
      { title: "It's Alive", year: '2013' },
    ]);
  });

  it('returns an empty list rather than throwing when there are none', async () => {
    const fetchFn = stubFetch([{ match: /release-groups/, json: { 'release-groups': [] } }]);
    expect(await fetchReleaseGroups('mbid-1', { fetchFn, minIntervalMs: 0 })).toEqual([]);
  });
});
