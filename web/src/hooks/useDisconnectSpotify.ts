import { useMutation, useQueryClient } from '@tanstack/react-query';
import { disconnectSpotify } from '../api/spotify';

export function useDisconnectSpotify() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: disconnectSpotify,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['spotify-status'] });
      qc.invalidateQueries({ queryKey: ['spotifyInterests'] });
    },
  });
}
