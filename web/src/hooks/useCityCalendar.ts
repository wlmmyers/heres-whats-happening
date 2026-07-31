import {
  keepPreviousData,
  type InfiniteData,
  useQueryClient,
  useInfiniteQuery,
} from '@tanstack/react-query';
import { getCityCalendar, type CalendarResponse, type PageParam } from '../api/calendar';
import { useAuth } from '../auth/useAuth';

type QueryKey = readonly ['city-calendar', string | undefined];

// Every event in a city, for the calendar's no-interests fallback. `enabled`
// keeps it idle until the caller knows the fallback applies; the query stays
// idle while the city is unknown.
export function useCityCalendar(cityId?: string) {
  const { user } = useAuth();
  const queryClient = useQueryClient();
  return useInfiniteQuery<
    CalendarResponse,
    Error,
    InfiniteData<CalendarResponse, PageParam>,
    QueryKey,
    PageParam
  >({
    queryKey: ['city-calendar', cityId],
    queryFn: async ({ pageParam }) => {
      const response = await getCityCalendar(cityId!, pageParam);
      // Seed the event query cache
      response.events.forEach((event) => {
        queryClient.setQueryData(['event', user?.id, event.id], event);
      });
      return response;
    },
    initialPageParam: undefined,
    getNextPageParam: (lastPage) => lastPage.next_cursor,
    getPreviousPageParam: () => null,
    placeholderData: keepPreviousData,
    enabled: !!cityId,
  });
}
