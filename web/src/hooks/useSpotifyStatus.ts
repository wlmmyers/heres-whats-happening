import { useQuery } from '@tanstack/react-query';
import { getSpotifyStatus } from '../api/spotify';
import { useAuth } from '../auth/useAuth';

export function useSpotifyStatus() {
  const { user } = useAuth();
  return useQuery({
    queryKey: ['spotify-status', user?.id],
    queryFn: getSpotifyStatus,
  });
}
