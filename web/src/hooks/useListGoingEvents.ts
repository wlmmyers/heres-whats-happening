import { useQuery } from '@tanstack/react-query';
import type { CalendarEvent } from '../api/calendar';
import { listGoingEvents } from '../api/eventGoing';
import { useAuth } from '../auth/useAuth';

export function listGoingEventsQueryKey(userId: string | undefined) {
  return ['event-going-events', userId] as const;
}

// Kept separate from useListGoing rather than derived from it: that query holds
// bare ids, and every card on the calendar reads it to colour its toggle.
export function useListGoingEvents() {
  const { user } = useAuth();
  return useQuery<CalendarEvent[]>({
    queryKey: listGoingEventsQueryKey(user?.id),
    queryFn: () => listGoingEvents(),
    enabled: !!user,
  });
}
