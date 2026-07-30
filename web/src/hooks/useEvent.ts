import { useQuery } from '@tanstack/react-query';
import { getEvent, type CalendarEvent } from '../api/calendar';
import { useAuth } from '../auth/useAuth';

export function useEvent(id: string | undefined) {
  const { user } = useAuth();
  return useQuery<CalendarEvent>({
    queryKey: ['event', user?.id, id],
    queryFn: () => getEvent(id!),
    enabled: !!id,
  });
}
