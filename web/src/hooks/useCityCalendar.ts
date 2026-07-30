import { useQuery, keepPreviousData } from '@tanstack/react-query';
import { getCityCalendar, type CalendarEvent } from '../api/calendar';

// Every event in a city, for the calendar's no-interests fallback. `enabled`
// keeps it idle until the caller knows the fallback applies; the query stays
// idle while the city is unknown.
export function useCityCalendar(
  cityId: string | undefined,
  from: string,
  to: string,
  enabled: boolean,
) {
  return useQuery<CalendarEvent[]>({
    queryKey: ['city-calendar', cityId, from, to],
    queryFn: () => getCityCalendar(cityId!, from, to),
    enabled: enabled && !!cityId,
    placeholderData: keepPreviousData,
  });
}
