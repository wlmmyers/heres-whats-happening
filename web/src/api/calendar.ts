import { apiFetch } from './client';

export interface MatchedBecause {
  performers: string[];
  genres: string[];
}

export type PageParam = string | undefined;

interface CalendarArtist {
  name: string;
  disambiguation?: string;
  mbid?: string;
  image?: ArtistImage;
  bio?: ArtistBio;
  tour?: ArtistTour;
}

interface BioSource {
  kind: string;
  title: string;
  url: string;
  revision_id: number;
  mbid: string;
}

interface ArtistBio {
  text: string;
  sources: BioSource[];
}

interface ArtistTour {
  text: string;
  sources: ArtistTourSources;
}

interface TourObserved {
  date: string;
  venue: string;
  city: string;
}

interface ArtistTourSources {
  name: string;
  blurb: string;
  setlist_url: string;
  songs: { name: string }[];
  observed: TourObserved;
}

interface ImageCredit {
  file: string;
  description_url: string;
  artist: string;
  credit: string;
  license: string;
  license_short_name: string;
  license_url: string;
  usage_terms: string;
  attribution_required: boolean;
}

interface ArtistImage {
  url: string;
  width: number;
  height: number;
  credit: ImageCredit;
}

export interface CalendarEvent {
  id: string;
  title: string;
  description?: string;
  starts_at: string;
  ends_at?: string;
  image_url?: string;
  url?: string;
  venue: { name: string; address?: string };
  score: number;
  matched_because: MatchedBecause;
  artist?: CalendarArtist;
}

export interface CalendarResponse {
  events: CalendarEvent[];
  // Omitted by the server (`json:"next_cursor,omitempty"`) on the last page,
  // which is what stops react-query from paging forever.
  next_cursor?: string;
}

export async function getCalendar(cursor?: string): Promise<CalendarResponse> {
  const now = new Date();
  now.setHours(0, 0, 0, 0);
  const params = cursor
    ? new URLSearchParams({ cursor })
    : new URLSearchParams({ starts_at: now.toISOString() });
  const result = await apiFetch<CalendarResponse>(
    `/me/calendar${params ? '?' + params.toString() : ''}`,
  );
  return result;
}

export async function getEvent(id: string): Promise<CalendarEvent> {
  return apiFetch<CalendarEvent>(`/events/${id}`);
}

// Every event in a city, unfiltered by match score. The calendar falls back to
// this when the user has nothing to match against yet.
export async function getCityCalendar(cityId: string, cursor?: string): Promise<CalendarResponse> {
  const now = new Date();
  now.setHours(0, 0, 0, 0);
  const params = cursor
    ? new URLSearchParams({ cursor })
    : new URLSearchParams({ starts_at: now.toISOString() });
  const result = await apiFetch<CalendarResponse>(
    `/calendar/${cityId}${params ? '?' + params.toString() : ''}`,
  );
  return result;
}
