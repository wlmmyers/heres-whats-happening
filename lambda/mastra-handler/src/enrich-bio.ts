import { bioAuthorAgent, type BioOutputSchema } from './mastra/agents/bio-author.agent.js';
import type { z } from 'zod';
import type { BioInfo } from './enrichment-schema.js';
import {
  fetchReleaseGroups,
  fetchWikipediaExtract,
  type ReleaseGroup,
  type WikipediaExtract,
} from './mastra/tools/artist-facts.tool.js';
import { wikimediaClient } from './mastra/tools/wikimedia.tool.js';

type BioOutput = z.infer<typeof BioOutputSchema>;

export interface BioDeps {
  resolveQid(mbid: string): Promise<string | null>;
  fetchExtract(qid: string): Promise<WikipediaExtract | null>;
  fetchAlbums(mbid: string): Promise<ReleaseGroup[]>;
  writeBio(prompt: string): Promise<BioOutput>;
  model: string;
}

export interface ArtistRef {
  mbid: string;
  name: string;
  disambiguation?: string;
}

function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/**
 * Wikipedia prose for the narrative, MusicBrainz release-groups for the facts.
 * NEVER throws: the orchestrator emits the enriched message regardless of what
 * failed, and a throw here would take a perfectly good event down with it.
 */
export async function enrichBio(deps: BioDeps, artist: ArtistRef): Promise<BioInfo> {
  // No MBID means no Wikidata hop is possible. A real answer, not a failure —
  // and it spends no calls.
  if (!artist.mbid) {
    return { status: 'none', reason: 'no MusicBrainz match, so no Wikidata entity to resolve' };
  }

  try {
    const qid = await deps.resolveQid(artist.mbid);
    if (!qid) return { status: 'none', reason: `no Wikidata entity for MBID ${artist.mbid}` };

    const extract = await deps.fetchExtract(qid);
    if (!extract) return { status: 'none', reason: `no English Wikipedia article for ${qid}` };

    // Albums are best-effort: a bio from prose alone is still worth having.
    let albums: ReleaseGroup[] = [];
    try {
      albums = await deps.fetchAlbums(artist.mbid);
    } catch (e) {
      console.log(
        JSON.stringify({ msg: 'release-groups-failed', mbid: artist.mbid, error: message(e) }),
      );
    }

    const who = artist.disambiguation ? `${artist.name} (${artist.disambiguation})` : artist.name;
    const albumList = albums.length
      ? albums.map((a) => `- ${a.title} (${a.year})`).join('\n')
      : '(no album data available)';

    const out = await deps.writeBio(
      `Artist: ${who}\n\nWikipedia extract:\n${extract.text}\n\nAlbums (authoritative):\n${albumList}`,
    );

    if (!out.usable || !out.bio.trim()) {
      return { status: 'none', reason: 'source material too thin for an accurate bio' };
    }

    return {
      status: 'ok',
      bio_md: out.bio.trim(),
      model: deps.model,
      sources: [
        {
          kind: 'wikipedia',
          title: extract.title,
          url: extract.url,
          revision_id: extract.revisionId,
        },
        { kind: 'musicbrainz', mbid: artist.mbid },
      ],
    };
  } catch (e) {
    return { status: 'error', reason: message(e) };
  }
}

/** Production deps. */
export function prodBioDeps(): BioDeps {
  const model = process.env.LLM_MODEL || 'anthropic/claude-sonnet-4-5';
  return {
    resolveQid: (mbid) => wikimediaClient.resolveQid(mbid),
    fetchExtract: (qid) => fetchWikipediaExtract(qid),
    fetchAlbums: (mbid) => fetchReleaseGroups(mbid),
    writeBio: async (prompt) => {
      const res = await bioAuthorAgent.generate([{ role: 'user', content: prompt }]);
      return (res.object as BioOutput | undefined) ?? { bio: '', usable: false };
    },
    model,
  };
}
