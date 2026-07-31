import { useQuery } from '@tanstack/react-query';
import { getSpotifyStatus } from '../api/spotify';
import { useAuth } from '../auth/useAuth';

export function useSpotifyStatus() {
  const { user } = useAuth();
  return useQuery({
    queryKey: ['spotify-status', user?.id],
    queryFn: getSpotifyStatus,
    // Keyed on user.id, so running before AuthProvider resolves would cache
    // under `undefined` and refetch under the real id a moment later.
    enabled: !!user,
  });
}
