import { apiFetch } from './client';

export interface SearchResultVenue {
  name: string;
}

export interface SearchResult {
  id: string;
  title: string;
  starts_at: string;
  image_url?: string;
  segment?: string;
  venue: SearchResultVenue;
}

export interface SearchResponse {
  results: SearchResult[];
}

export async function searchEvents(cityId: string, q: string): Promise<SearchResponse> {
  return apiFetch<SearchResponse>(`/search/${cityId}/events?q=${encodeURIComponent(q)}`);
}
