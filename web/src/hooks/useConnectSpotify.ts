import { useMutation } from '@tanstack/react-query';
import { startSpotifyConnect } from '../api/spotify';

export function useConnectSpotify() {
  return useMutation({
    mutationFn: startSpotifyConnect,
    onSuccess: (authorizeURL) => {
      window.location.assign(authorizeURL);
    },
  });
}
