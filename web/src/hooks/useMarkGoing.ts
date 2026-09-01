import { useMutation, useQueryClient } from '@tanstack/react-query';
import { markEventGoing } from '../api/eventGoing';
import { useAuth } from '../auth/useAuth';
import { listGoingQueryKey } from './useListGoing';
import { listGoingEventsQueryKey } from './useListGoingEvents';

export function useMarkGoing() {
  const qc = useQueryClient();
  const { user } = useAuth();

  return useMutation({
    mutationFn: (id: string) => markEventGoing(id),
    onMutate: async (id: string) => {
      const listGoingKey = listGoingQueryKey(user?.id);
      await qc.cancelQueries({ queryKey: listGoingKey });
      qc.setQueryData<string[]>(listGoingKey, (old) => {
        const newSet = new Set(old);
        newSet.add(id);
        return [...newSet];
      });
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: listGoingQueryKey(user?.id) });
      // The optimistic update above only touches the id list; the rendered list
      // holds whole events, which only the server can supply for a new id.
      qc.invalidateQueries({ queryKey: listGoingEventsQueryKey(user?.id) });
    },
  });
}
