import { useMutation, useQueryClient } from '@tanstack/react-query';
import { type CalendarEvent } from '../api/calendar';
import { markNotInterested } from '../api/notInterested';
import { useAuth } from '../auth/useAuth';
import { calendarQueryKey } from './useCalendar';

export function useMarkNotInterested(from: string, to: string) {
  const qc = useQueryClient();
  const { user } = useAuth();
  const calendarKey = calendarQueryKey(user?.id, from, to);

  return useMutation({
    mutationFn: (id: string) => markNotInterested(id),
    onMutate: async (id: string) => {
      await qc.cancelQueries({ queryKey: calendarKey });
      const prev = qc.getQueryData<CalendarEvent[]>(calendarKey);
      qc.setQueryData<CalendarEvent[]>(calendarKey, (old) =>
        (old ?? []).filter((e) => e.id !== id),
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
