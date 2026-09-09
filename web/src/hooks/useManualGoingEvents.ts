import { useQuery } from '@tanstack/react-query';
import { listManualGoingEvents, type ManualGoingEvent } from '../api/manualGoingEvents';
import { useAuth } from '../auth/useAuth';

export function manualGoingEventsQueryKey(userId: string | undefined) {
  return ['manual-going-events', userId] as const;
}

// The shows the user typed in by hand. Kept as its own query rather than
// folded into useListGoingEvents: these rows come from a different table and a
// different endpoint, and GoingWentList merges the two at render time.
export function useManualGoingEvents() {
  const { user } = useAuth();
  return useQuery<ManualGoingEvent[]>({
    queryKey: manualGoingEventsQueryKey(user?.id),
    queryFn: listManualGoingEvents,
    // Keyed on user.id — stay idle until it is known. See useSpotifyStatus.
    enabled: !!user,
  });
}
