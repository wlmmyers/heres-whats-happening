import { useMutation, useQueryClient } from '@tanstack/react-query';
import { resetNotInterested } from '../api/notInterested';

export function useResetNotInterested() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: resetNotInterested,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['calendar'] });
    },
  });
}
