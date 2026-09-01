import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useAuth } from '../auth/useAuth';
import { listGoingQueryKey } from './useListGoing';
import { listGoingEventsQueryKey } from './useListGoingEvents';
import { resetEventGoing } from '../api/eventGoing';
import type { CalendarEvent } from '../api/calendar';

export function useMarkNotGoing() {
  const qc = useQueryClient();
  const { user } = useAuth();

  return useMutation({
    mutationFn: (id: string) => resetEventGoing(id),
    onMutate: async (id: string) => {
      const listGoingKey = listGoingQueryKey(user?.id);
      await qc.cancelQueries({ queryKey: listGoingKey });
      qc.setQueryData<string[]>(listGoingKey, (old) => old && old.filter((e) => e !== id));

      // Removal needs no server round trip to render, so drop the row from the
      // list too and the sidebar updates with the card's toggle.
      const listGoingEventsKey = listGoingEventsQueryKey(user?.id);
      await qc.cancelQueries({ queryKey: listGoingEventsKey });
      qc.setQueryData<CalendarEvent[]>(
        listGoingEventsKey,
        (old) => old && old.filter((e) => e.id !== id),
      );
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: listGoingQueryKey(user?.id) });
      qc.invalidateQueries({ queryKey: listGoingEventsQueryKey(user?.id) });
    },
  });
}
