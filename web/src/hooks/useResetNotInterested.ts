import { useMutation, useQueryClient } from '@tanstack/react-query';
import { resetNotInterested } from '../api/notInterested';
import { listNotInterestedQueryKey } from './useListNotInterested';
import { useAuth } from '../auth/useAuth';

export function useResetNotInterested() {
  const { user } = useAuth();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: resetNotInterested,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['calendar'] });
      qc.invalidateQueries({ queryKey: listNotInterestedQueryKey(user?.id) });
    },
  });
}
