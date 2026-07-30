import { keepPreviousData, useQuery, useQueryClient } from '@tanstack/react-query';
import { getCalendar, type CalendarEvent } from '../api/calendar';
import { useAuth } from '../auth/useAuth';

export function calendarQueryKey(userId: string | undefined, from: string, to: string) {
  return ['calendar', userId, from, to] as const;
}

export function useCalendar(from: string, to: string) {
  const { user } = useAuth();
  const queryClient = useQueryClient();

  return useQuery<CalendarEvent[]>({
    queryKey: calendarQueryKey(user?.id, from, to),
    queryFn: async () => {
      const events = await getCalendar(from, to);
      // Seed the event query cache
      events.forEach((event) => {
        queryClient.setQueryData(['event', user?.id, event.id], event);
      });
      return events;
    },
    placeholderData: keepPreviousData,
  });
}
