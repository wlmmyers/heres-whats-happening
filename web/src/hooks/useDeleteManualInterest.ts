import { useMutation, useQueryClient } from '@tanstack/react-query';
import { deleteManualInterest } from '../api/manualInterests';
import { useManualInterests } from './useManualInterests';

export function useDeleteManualInterest() {
  const qc = useQueryClient();
  const { data: interests = [] } = useManualInterests();
  return useMutation({
    // Callers remove by display value (e.g. from TagList); resolve it to the
    // interest id here. Reuses the cached useManualInterests query, so no extra
    // request is made.
    mutationFn: (value: string) => {
      const target = interests.find((i) => i.value === value);
      if (!target) return Promise.resolve();
      return deleteManualInterest(target.id);
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['interests'] }),
  });
}
