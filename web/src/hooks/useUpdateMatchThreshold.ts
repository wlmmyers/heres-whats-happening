import { useMutation, useQueryClient } from '@tanstack/react-query';
import { updateMatchThreshold } from '../api/match';

export function useUpdateMatchThreshold() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (threshold: number) => updateMatchThreshold(threshold),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['me'] });
      qc.invalidateQueries({ queryKey: ['calendar'] });
    },
  });
}
