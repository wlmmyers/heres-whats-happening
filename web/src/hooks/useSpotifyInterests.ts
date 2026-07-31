import { useQuery } from '@tanstack/react-query';
import { listSpotifyInterests, type SpotifyInterestGroup } from '../api/spotifyInterests';
import { useAuth } from '../auth/useAuth';

export function useSpotifyInterests() {
  const { user } = useAuth();
  return useQuery<SpotifyInterestGroup[]>({
    queryKey: ['spotifyInterests', user?.id],
    queryFn: listSpotifyInterests,
    // Keyed on user.id — stay idle until it is known. See useSpotifyStatus.
    enabled: !!user,
  });
}
