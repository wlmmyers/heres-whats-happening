import { useMutation, useQueryClient } from '@tanstack/react-query';
import { updateManualGoingEvent, type ManualGoingEventInput } from '../api/manualGoingEvents';

// No UI calls this yet — editing a manual event is not in the sidebar. It
// completes the CRUD set against the endpoints, so an edit affordance later is
// a component, not another round trip through the API layer.
export function useUpdateManualGoingEvent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, input }: { id: string; input: ManualGoingEventInput }) =>
      updateManualGoingEvent(id, input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['manual-going-events'] }),
  });
}
