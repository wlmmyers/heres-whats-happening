import { apiFetch } from './client';

export interface MatchedBecause {
  performers: string[];
  genres: string[];
}

export type PageParam = string | undefined;

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
}

export interface CalendarResponse {
  events: CalendarEvent[];
  // Omitted by the server (`json:"next_cursor,omitempty"`) on the last page,
  // which is what stops react-query from paging forever.
  next_cursor?: string;
}

export async function getCalendar(cursor?: string): Promise<CalendarResponse> {
  const params = cursor ? new URLSearchParams({ cursor }) : null;
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
  const params = cursor ? new URLSearchParams({ cursor }) : null;
  const result = await apiFetch<CalendarResponse>(
    `/calendar/${cityId}${params ? '?' + params.toString() : ''}`,
  );
  return result;
}
