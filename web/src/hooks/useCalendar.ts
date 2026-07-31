import {
  keepPreviousData,
  useInfiniteQuery,
  useQueryClient,
  type InfiniteData,
} from '@tanstack/react-query';
import { getCalendar, type CalendarResponse, type PageParam } from '../api/calendar';
import { useAuth } from '../auth/useAuth';

type QueryKey = readonly ['calendar', string | undefined];

export function calendarQueryKey(userId: string | undefined) {
  return ['calendar', userId] as const;
}

export function useCalendar() {
  const { user } = useAuth();
  const queryClient = useQueryClient();

  return useInfiniteQuery<
    CalendarResponse,
    Error,
    InfiniteData<CalendarResponse, PageParam>,
    QueryKey,
    PageParam
  >({
    queryKey: ['calendar', user?.id],
    queryFn: async ({ pageParam }) => {
      const response = await getCalendar(pageParam);
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
  });
}
