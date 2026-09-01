import { useQuery } from '@tanstack/react-query';
import { useAuth } from '../auth/useAuth';
import { listNotInterested } from '../api/notInterested';

export function listNotInterestedQueryKey(userId: string | undefined) {
  return ['not-interested', userId] as const;
}

export function useListNotInterested() {
  const { user } = useAuth();
  return useQuery<string[]>({
    queryKey: listNotInterestedQueryKey(user?.id),
    queryFn: () => listNotInterested(),
    enabled: !!user,
  });
}
