import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import SettingsPage from './SettingsPage';

vi.mock('../api/interests', () => ({
  listInterests: vi.fn(),
  createInterest: vi.fn(),
  deleteInterest: vi.fn(),
}));
vi.mock('../api/spotify', () => ({
  startSpotifyConnect: vi.fn(),
  disconnectSpotify: vi.fn(),
}));
vi.mock('../api/ical', () => ({
  createIcalToken: vi.fn(),
  revokeIcalToken: vi.fn(),
}));

import * as interestsApi from '../api/interests';
import * as icalApi from '../api/ical';
import * as spotifyApi from '../api/spotify';

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/settings']}>
        <Routes>
          <Route path="/settings" element={<SettingsPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  (interestsApi.listInterests as ReturnType<typeof vi.fn>).mockResolvedValue([]);
});

describe('SettingsPage', () => {
  it('navigates to the Spotify authorize URL when Connect Spotify is clicked', async () => {
    (spotifyApi.startSpotifyConnect as ReturnType<typeof vi.fn>).mockResolvedValueOnce(
      'https://accounts.spotify.com/authorize?x=1',
    );
    const assign = vi.fn();
    const originalLocation = window.location;
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { ...originalLocation, assign },
    });

    try {
      renderPage();
      await userEvent.click(await screen.findByRole('button', { name: /connect spotify/i }));
      await waitFor(() =>
        expect(assign).toHaveBeenCalledWith('https://accounts.spotify.com/authorize?x=1'),
      );
    } finally {
      Object.defineProperty(window, 'location', {
        configurable: true,
        value: originalLocation,
      });
    }
  });

  it('generates an iCal URL on demand and reveals it', async () => {
    (icalApi.createIcalToken as ReturnType<typeof vi.fn>).mockResolvedValueOnce({ url: 'http://x/ical/abc.ics' });
    renderPage();
    await userEvent.click(screen.getByRole('button', { name: /generate calendar url/i }));
    await waitFor(() => expect(screen.getByText('http://x/ical/abc.ics')).toBeInTheDocument());
  });

  it('lets the user add a manual interest', async () => {
    (interestsApi.createInterest as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      id: 'i1', value: 'theater', normalized_value: 'theater', weight: 1, created_at: '',
    });
    (interestsApi.listInterests as ReturnType<typeof vi.fn>)
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce([{ id: 'i1', value: 'theater', normalized_value: 'theater', weight: 1, created_at: '' }]);
    renderPage();
    await userEvent.type(screen.getByPlaceholderText(/add an interest/i), 'theater{enter}');
    await waitFor(() => expect(interestsApi.createInterest).toHaveBeenCalledWith('theater'));
    await waitFor(() => expect(screen.getByText('theater')).toBeInTheDocument());
  });
});
