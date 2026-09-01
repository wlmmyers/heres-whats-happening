import { useQuery } from '@tanstack/react-query';
import { listGoing } from '../api/eventGoing';
import { useAuth } from '../auth/useAuth';

export function listGoingQueryKey(userId: string | undefined) {
  return ['event-going', userId] as const;
}

export function useListGoing() {
  const { user } = useAuth();
  return useQuery<string[]>({
    queryKey: listGoingQueryKey(user?.id),
    queryFn: () => listGoing(),
    enabled: !!user,
  });
}
