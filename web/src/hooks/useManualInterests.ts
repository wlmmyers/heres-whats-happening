import { useQuery } from '@tanstack/react-query';
import { listManualInterests, type Interest } from '../api/manualInterests';
import { useAuth } from '../auth/useAuth';

export function useManualInterests() {
  const { user } = useAuth();
  return useQuery<Interest[]>({
    queryKey: ['interests', user?.id],
    queryFn: listManualInterests,
  });
}
