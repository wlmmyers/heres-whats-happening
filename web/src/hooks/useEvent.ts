import { useQuery } from '@tanstack/react-query';
import { getEvent, type CalendarEvent } from '../api/calendar';
import { useAuth } from '../auth/useAuth';

export function useEvent(id: string | undefined) {
  const { user } = useAuth();
  return useQuery<CalendarEvent>({
    queryKey: ['event', user?.id, id],
    queryFn: () => getEvent(id!),
    // Keyed on user.id as well as id — stay idle until both are known, or the
    // useCalendar-seeded entry under the real id is missed. See useSpotifyStatus.
    enabled: !!id && !!user,
  });
}
