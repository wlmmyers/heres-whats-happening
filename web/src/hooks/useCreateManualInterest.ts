import { useMutation, useQueryClient } from '@tanstack/react-query';
import { createManualInterest } from '../api/manualInterests';

export function useCreateManualInterest() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (value: string) => createManualInterest(value),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['interests'] }),
  });
}
