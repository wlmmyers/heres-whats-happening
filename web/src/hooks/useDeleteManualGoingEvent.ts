import { useMutation, useQueryClient } from '@tanstack/react-query';
import { deleteManualGoingEvent } from '../api/manualGoingEvents';

export function useDeleteManualGoingEvent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteManualGoingEvent(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['manual-going-events'] }),
  });
}
