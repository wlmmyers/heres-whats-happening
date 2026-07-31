import { useMutation, useQueryClient, type InfiniteData } from '@tanstack/react-query';
import { type CalendarResponse, type PageParam } from '../api/calendar';
import { markNotInterested } from '../api/notInterested';
import { useAuth } from '../auth/useAuth';
import { calendarQueryKey } from './useCalendar';

// The calendar is an infinite query, so its cache entry is pages of
// CalendarResponse rather than a flat event list. The dismissed event has to be
// filtered out of every page that is already loaded.
type CalendarPages = InfiniteData<CalendarResponse, PageParam>;

export function useMarkNotInterested() {
  const qc = useQueryClient();
  const { user } = useAuth();
  const calendarKey = calendarQueryKey(user?.id);

  return useMutation({
    mutationFn: (id: string) => markNotInterested(id),
    onMutate: async (id: string) => {
      await qc.cancelQueries({ queryKey: calendarKey });
      const prev = qc.getQueryData<CalendarPages>(calendarKey);
      qc.setQueryData<CalendarPages>(
        calendarKey,
        (old) =>
          old && {
            ...old,
            pages: old.pages.map((page) => ({
              ...page,
              events: page.events.filter((e) => e.id !== id),
            })),
          },
      );
      return { prev };
    },
    onError: (_err, _id, ctx) => {
      if (ctx?.prev) qc.setQueryData(calendarKey, ctx.prev);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ['calendar'] });
    },
  });
}
