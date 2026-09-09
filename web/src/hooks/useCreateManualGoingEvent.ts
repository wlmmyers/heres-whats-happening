import { useMutation, useQueryClient } from '@tanstack/react-query';
import { createManualGoingEvent, type ManualGoingEventInput } from '../api/manualGoingEvents';

export function useCreateManualGoingEvent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: ManualGoingEventInput) => createManualGoingEvent(input),
    // Invalidated by prefix, so the refetch finds the query whichever user id
    // it is keyed on.
    onSuccess: () => qc.invalidateQueries({ queryKey: ['manual-going-events'] }),
  });
}
