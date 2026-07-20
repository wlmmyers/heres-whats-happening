import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import InterestsPage from './InterestsPage';

vi.mock('../api/manualInterests', () => ({
  listManualInterests: vi.fn(),
  createManualInterest: vi.fn(),
  deleteManualInterest: vi.fn(),
}));
vi.mock('../api/spotifyInterests', () => ({
  listSpotifyInterests: vi.fn(),
}));
vi.mock('../api/spotify', () => ({
  getSpotifyStatus: vi.fn(),
}));

import * as interestsApi from '../api/manualInterests';
import * as spotifyInterestsApi from '../api/spotifyInterests';
import * as spotifyApi from '../api/spotify';

function spotifyInterest(value: string) {
  return { id: value, value, normalized_value: value, weight: 1, created_at: '' };
}

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/interests']}>
        <Routes>
          <Route path="/interests" element={<InterestsPage />} />
          <Route path="/calendar" element={<div>calendar-route</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  (interestsApi.listManualInterests as ReturnType<typeof vi.fn>).mockResolvedValue([]);
  (spotifyInterestsApi.listSpotifyInterests as ReturnType<typeof vi.fn>).mockResolvedValue([]);
  (spotifyApi.getSpotifyStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ connected: false });
});

describe('InterestsPage', () => {
  it('lists existing interests and lets the user add one', async () => {
    (interestsApi.listManualInterests as ReturnType<typeof vi.fn>)
      .mockResolvedValueOnce([
        {
          id: 'i1',
          value: 'jazz',
          normalized_value: 'jazz',
          weight: 1,
          created_at: '',
        },
      ])
      .mockResolvedValueOnce([
        {
          id: 'i1',
          value: 'jazz',
          normalized_value: 'jazz',
          weight: 1,
          created_at: '',
        },
        {
          id: 'i2',
          value: 'theater',
          normalized_value: 'theater',
          weight: 1,
          created_at: '',
        },
      ]);
    (interestsApi.createManualInterest as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      id: 'i2',
      value: 'theater',
      normalized_value: 'theater',
      weight: 1,
      created_at: '',
    });
    renderPage();
    await waitFor(() => expect(screen.getByText('jazz')).toBeInTheDocument());

    await userEvent.type(screen.getByPlaceholderText(/add an interest/i), 'theater{enter}');
    await waitFor(() => expect(interestsApi.createManualInterest).toHaveBeenCalledWith('theater'));
    await waitFor(() => expect(screen.getByText('theater')).toBeInTheDocument());
  });

  it('Continue navigates to calendar', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByRole('button', { name: /continue/i })).toBeEnabled());
    await userEvent.click(screen.getByRole('button', { name: /continue/i }));
    expect(screen.getByText(/calendar-route/)).toBeInTheDocument();
  });
});

describe('InterestsPage — Spotify section', () => {
  it('renders a group per kind with its label and chips', async () => {
    (spotifyInterestsApi.listSpotifyInterests as ReturnType<typeof vi.fn>).mockResolvedValue([
      { kind: 'spotify_top_artist', label: 'Top artists', interests: [spotifyInterest('Alvvays')] },
      { kind: 'spotify_top_genre', label: 'Top genres', interests: [spotifyInterest('Shoegaze')] },
    ]);
    renderPage();

    await waitFor(() => expect(screen.getByText('Top artists')).toBeInTheDocument());
    expect(screen.getByText('Alvvays')).toBeInTheDocument();
    expect(screen.getByText('Top genres')).toBeInTheDocument();
    expect(screen.getByText('Shoegaze')).toBeInTheDocument();
  });

  it('renders Spotify chips read-only', async () => {
    (spotifyInterestsApi.listSpotifyInterests as ReturnType<typeof vi.fn>).mockResolvedValue([
      { kind: 'spotify_top_artist', label: 'Top artists', interests: [spotifyInterest('Alvvays')] },
    ]);
    renderPage();

    await waitFor(() => expect(screen.getByText('Alvvays')).toBeInTheDocument());
    expect(screen.queryByLabelText('Remove Alvvays')).not.toBeInTheDocument();
  });

  it('collapses groups longer than 20 and expands on click', async () => {
    const many = Array.from({ length: 25 }, (_, i) => spotifyInterest(`artist-${i}`));
    (spotifyInterestsApi.listSpotifyInterests as ReturnType<typeof vi.fn>).mockResolvedValue([
      { kind: 'spotify_saved_song_artist', label: 'Artists from your saved songs', interests: many },
    ]);
    renderPage();

    await waitFor(() => expect(screen.getByText('artist-0')).toBeInTheDocument());
    expect(screen.queryByText('artist-24')).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: /show all \(25\)/i }));
    expect(screen.getByText('artist-24')).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: /show less/i }));
    expect(screen.queryByText('artist-24')).not.toBeInTheDocument();
  });

  it('hides the section entirely when there are no groups and Spotify is not connected', async () => {
    (spotifyInterestsApi.listSpotifyInterests as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    (spotifyApi.getSpotifyStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ connected: false });
    renderPage();

    await waitFor(() => expect(screen.getByRole('button', { name: /continue/i })).toBeEnabled());
    expect(screen.queryByText(/from your spotify/i)).not.toBeInTheDocument();
  });

  it('shows a pending message when connected but nothing scraped yet', async () => {
    (spotifyInterestsApi.listSpotifyInterests as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    (spotifyApi.getSpotifyStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ connected: true });
    renderPage();

    await waitFor(() =>
      expect(screen.getByText(/haven't pulled your listening history yet/i)).toBeInTheDocument(),
    );
  });
});
