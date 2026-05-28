import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import SpotifyCallbackPage from './SpotifyCallbackPage';

vi.mock('../api/spotify', () => ({
  exchangeSpotifyCode: vi.fn(),
}));

import * as spotifyApi from '../api/spotify';

function renderAt(url: string) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <Routes>
        <Route path="/integrations/spotify/callback" element={<SpotifyCallbackPage />} />
        <Route path="/calendar" element={<div>calendar-route</div>} />
        <Route path="/settings" element={<div>settings-route</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
});

describe('SpotifyCallbackPage', () => {
  it('exchanges the code and shows the success state', async () => {
    (spotifyApi.exchangeSpotifyCode as ReturnType<typeof vi.fn>).mockResolvedValueOnce(undefined);
    renderAt('/integrations/spotify/callback?code=THE-CODE&state=THE-STATE');

    await waitFor(() =>
      expect(spotifyApi.exchangeSpotifyCode).toHaveBeenCalledWith('THE-CODE', 'THE-STATE'),
    );
    await waitFor(() => expect(screen.getByText(/spotify connected/i)).toBeInTheDocument());
  });

  it('shows an error if Spotify returned an error param', async () => {
    renderAt('/integrations/spotify/callback?error=access_denied');
    await waitFor(() => expect(screen.getByText(/spotify connection failed/i)).toBeInTheDocument());
    expect(screen.getByText('access_denied')).toBeInTheDocument();
    expect(spotifyApi.exchangeSpotifyCode).not.toHaveBeenCalled();
  });

  it('shows an error if the exchange call fails', async () => {
    (spotifyApi.exchangeSpotifyCode as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new Error('state_mismatch'),
    );
    renderAt('/integrations/spotify/callback?code=C&state=S');
    await waitFor(() => expect(screen.getByText(/spotify connection failed/i)).toBeInTheDocument());
    expect(screen.getByText('state_mismatch')).toBeInTheDocument();
  });
});
