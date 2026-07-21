import { apiFetch } from './client';
import loggedOutData from './logged-out-calendar-data.json';

export interface MatchedBecause {
  performers: string[];
  genres: string[];
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
}

export async function getCalendar(
  from: string,
  to: string,
  loggedOut = false,
): Promise<CalendarEvent[]> {
  if (loggedOut) {
    return (loggedOutData as { events: CalendarEvent[] }).events;
  }
  const params = new URLSearchParams({ from, to });
  const out = await apiFetch<{ events: CalendarEvent[] }>(`/me/calendar?${params.toString()}`);
  return out.events;
}

export async function getEvent(id: string): Promise<CalendarEvent> {
  return apiFetch<CalendarEvent>(`/events/${id}`);
}
