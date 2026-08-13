import type { z } from 'zod';
import type { TourInfo } from './enrichment-schema.js';
import { tourBlurbAgent, type TourBlurbSchema } from './mastra/agents/tour-blurb.agent.js';
import { createSetlistFmClient, type RecentSetlist } from './mastra/tools/setlistfm.tool.js';
import type { ArtistRef } from './enrich-bio.js';

type TourBlurb = z.infer<typeof TourBlurbSchema>;

export interface TourDeps {
  recentSetlist(mbid: string): Promise<RecentSetlist | null>;
  writeBlurb(prompt: string): Promise<TourBlurb>;
  model: string;
}

export interface EventRef {
  venue: string;
  date: string;
}

function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/**
 * setlist.fm has no set times — it is a post-show archive — so this records the
 * band's most recent qualifying setlist and a blurb grounded ONLY in that.
 * NEVER throws.
 */
export async function enrichTour(
  deps: TourDeps,
  artist: ArtistRef,
  event: EventRef,
): Promise<TourInfo> {
  // setlist.fm is keyed by MBID. No MBID, no request spent.
  if (!artist.mbid) {
    return { status: 'none', reason: 'no MusicBrainz match, so no setlist.fm lookup is possible' };
  }

  let setlist: RecentSetlist | null;
  try {
    setlist = await deps.recentSetlist(artist.mbid);
  } catch (e) {
    return { status: 'error', reason: message(e) };
  }
  if (!setlist) {
    return { status: 'none', reason: 'no setlist with songs within the recency window' };
  }

  const base: TourInfo = {
    status: 'ok',
    tour_name: setlist.tourName,
    songs: setlist.songs,
    observed_date: setlist.observedDate,
    observed_venue: setlist.observedVenue,
    observed_city: setlist.observedCity,
    setlist_url: setlist.setlistUrl,
  };

  // The blurb is a bonus. A failure here leaves the setlist, which is worth
  // serving on its own — hence status stays 'ok' with blurb absent.
  try {
    const out = await deps.writeBlurb(
      [
        `Band: ${artist.name}`,
        `Upcoming show: ${event.venue} on ${event.date}`,
        setlist.tourName ? `Tour: ${setlist.tourName}` : 'Tour: (none listed)',
        `Most recent setlist: ${setlist.observedDate} at ${setlist.observedVenue ?? 'unknown venue'}, ${setlist.observedCity ?? 'unknown city'}`,
        `Songs played: ${setlist.songs.length}`,
        `First few: ${setlist.songs
          .slice(0, 5)
          .map((s) => s.name)
          .join(', ')}`,
      ].join('\n'),
    );
    if (out.usable && out.blurb.trim()) {
      base.blurb = out.blurb.trim();
      base.blurb_model = deps.model;
    }
  } catch (e) {
    console.log(JSON.stringify({ msg: 'tour-blurb-failed', mbid: artist.mbid, error: message(e) }));
  }

  return base;
}

/** Production deps. Throws if the key was never loaded — the orchestrator
 * catches it and records status 'error'. */
export function prodTourDeps(apiKey: string): TourDeps {
  const client = createSetlistFmClient({ apiKey });
  const model = process.env.LLM_MODEL || 'anthropic/claude-sonnet-4-5';
  return {
    recentSetlist: (mbid) => client.recentSetlist(mbid),
    writeBlurb: async (prompt) => {
      const res = await tourBlurbAgent.generate([{ role: 'user', content: prompt }]);
      return (res.object as TourBlurb | undefined) ?? { blurb: '', usable: false };
    },
    model,
  };
}
