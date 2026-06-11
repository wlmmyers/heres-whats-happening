import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
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
  getSpotifyStatus: vi.fn(),
}));
vi.mock('../api/ical', () => ({
  createIcalToken: vi.fn(),
  revokeIcalToken: vi.fn(),
}));
vi.mock('../api/auth', () => ({
  getMe: vi.fn(),
}));
vi.mock('../api/match', () => ({
  updateMatchThreshold: vi.fn(),
  MIN_THRESHOLD: 0.2,
  MAX_THRESHOLD: 0.6,
}));
vi.mock('../api/notInterested', () => ({
  markNotInterested: vi.fn(),
  resetNotInterested: vi.fn(),
}));

import * as interestsApi from '../api/interests';
import * as icalApi from '../api/ical';
import * as spotifyApi from '../api/spotify';
import * as authApi from '../api/auth';
import * as matchApi from '../api/match';
import * as niApi from '../api/notInterested';

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
  (spotifyApi.getSpotifyStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ connected: false });
  (authApi.getMe as ReturnType<typeof vi.fn>).mockResolvedValue({
    id: 'u1', email: 'a@x', score_threshold: 0.3,
  });
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

  it('shows only the Disconnect button when Spotify is connected', async () => {
    (spotifyApi.getSpotifyStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ connected: true });
    renderPage();
    expect(await screen.findByRole('button', { name: /disconnect/i })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /connect spotify/i })).not.toBeInTheDocument();
  });

  it('shows only the Connect button when Spotify is not connected', async () => {
    (spotifyApi.getSpotifyStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ connected: false });
    renderPage();
    expect(await screen.findByRole('button', { name: /connect spotify/i })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /disconnect/i })).not.toBeInTheDocument();
  });

  it('resets the not-interested list after confirming', async () => {
    (niApi.resetNotInterested as ReturnType<typeof vi.fn>).mockResolvedValueOnce(undefined);
    renderPage();

    await userEvent.click(
      await screen.findByRole('button', { name: /reset not-interested list/i }),
    );
    await userEvent.click(screen.getByRole('button', { name: 'Confirm' }));

    await waitFor(() => expect(niApi.resetNotInterested).toHaveBeenCalled());
  });

  it('confirms before updating the match threshold', async () => {
    (matchApi.updateMatchThreshold as ReturnType<typeof vi.fn>).mockResolvedValueOnce(undefined);
    renderPage();

    // Slider initialises from score_threshold (0.3 -> 30%).
    const slider = await screen.findByRole('slider', { name: /match sensitivity/i });
    expect(slider).toHaveValue('30');
    expect(screen.getByRole('button', { name: /save threshold/i })).toBeDisabled();

    // Move to 45% and save.
    fireEvent.change(slider, { target: { value: '45' } });
    await userEvent.click(screen.getByRole('button', { name: /save threshold/i }));

    // Confirm dialog appears; confirming calls the API with the fraction.
    await userEvent.click(screen.getByRole('button', { name: 'Confirm' }));
    await waitFor(() =>
      expect(matchApi.updateMatchThreshold).toHaveBeenCalledWith(0.45),
    );
  });
});
