import { useQuery } from '@tanstack/react-query';
import { getCityVenues, type Venue } from '../api/venues';

// Keyed on the city alone, like useCityCalendar: the response is identical for
// every caller. Stays idle while the city is unknown.
export function useCityVenues(cityId?: string) {
  return useQuery<Venue[]>({
    queryKey: ['city-venues', cityId],
    queryFn: async () => (await getCityVenues(cityId!)).venues,
    enabled: !!cityId,
  });
}
