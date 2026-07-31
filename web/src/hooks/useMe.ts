import { useQuery } from '@tanstack/react-query';
import { getMe } from '../api/auth';
import { useAuth } from '../auth/useAuth';

export function useMe() {
  const { user } = useAuth();
  return useQuery({
    queryKey: ['me', user?.id],
    queryFn: getMe,
    // Keyed on user.id — stay idle until it is known. See useSpotifyStatus.
    enabled: !!user,
  });
}
