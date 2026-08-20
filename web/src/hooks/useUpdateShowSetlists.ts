import { useMutation, useQueryClient } from '@tanstack/react-query';
import { updateShowSetlists } from '../api/showSetlists';

export function useUpdateShowSetlists() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (showSetlists: boolean) => updateShowSetlists(showSetlists),
    // Only /me carries this flag; matching and the calendar are unaffected.
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['me'] });
    },
  });
}
