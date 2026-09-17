import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { searchEvents, type SearchResponse } from '../api/search';

// Mirrors the server's floor. Spread-counted so it is runes, not UTF-16 code
// units: three CJK characters are a valid query.
export const SEARCH_MIN_LENGTH = 3;

/**
 * Typeahead search over a city's upcoming events.
 *
 * Not keyed on user id, unlike the user-scoped hooks: results are city-scoped
 * and identical for every user in that city, so this follows useCityCalendar.
 */
export function useEventSearch(cityId: string | undefined, query: string) {
  const trimmed = query.trim();
  return useQuery<SearchResponse>({
    queryKey: ['event-search', cityId, trimmed],
    queryFn: () => searchEvents(cityId!, trimmed),
    enabled: !!cityId && [...trimmed].length >= SEARCH_MIN_LENGTH,
    // Without this the list blanks on every new query and the dropdown strobes
    // as the user types.
    placeholderData: keepPreviousData,
    staleTime: 30_000,
  });
}
