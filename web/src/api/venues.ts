import { apiFetch } from './client';

// Mirrors `venue` in internal/http/handlers/venues.go. Everything optional here
// is `omitempty` on the wire: sources fill in only what they know.
export interface Venue {
  id: string;
  name: string;
  address?: string;
  lat?: number;
  lng?: number;
  website_url?: string;
  // Live music events still to come (plus unclassified ones, as on the
  // calendar). Always at least 1: venues with none are not listed.
  upcoming_event_count: number;
  next_event_at: string;
}

export interface VenuesResponse {
  venues: Venue[];
}

// Every venue in a city with upcoming music, busiest first. Unpaginated.
export async function getCityVenues(cityId: string): Promise<VenuesResponse> {
  // Hardcoded to match the calendar.
  const params = new URLSearchParams({ segment: 'music' });
  return apiFetch<VenuesResponse>(`/venues/${cityId}?${params.toString()}`);
}
