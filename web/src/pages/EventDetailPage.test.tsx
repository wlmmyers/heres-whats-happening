import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import EventDetailPage from './EventDetailPage';
import * as s from './EventDetailPage.css';
import type { CalendarEvent } from '../api/calendar';

vi.mock('../api/calendar', () => ({
  getCalendar: vi.fn(),
  getEvent: vi.fn(),
}));
vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));

import * as calApi from '../api/calendar';
import { useAuth } from '../auth/useAuth';

function renderAt(path: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/events/:id" element={<EventDetailPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(useAuth).mockReturnValue({
    status: 'authenticated',
    user: { id: 'u1', email: 'a@x', city_id: 'city-1', confirmed: true },
    login: vi.fn(),
    signup: vi.fn(),
    logout: vi.fn(),
    refreshUser: vi.fn(),
  });
});

// ICU pads its range patterns with thin and narrow no-break spaces; compare
// against plain spaces so the expectations stay readable.
function dateLine(container: HTMLElement) {
  return container.querySelector(`.${s.date}`)?.textContent?.replace(/[\u2009\u202f\u00a0]/g, ' ');
}

describe('EventDetailPage', () => {
  it('shows only the start time when the event has no ends_at', async () => {
    (calApi.getEvent as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      id: 'e1',
      title: 'PB Live',
      starts_at: '2026-06-15T20:00:00Z',
      venue: { name: 'The Bowl' },
      score: 0,
      matched_because: { performers: [], genres: [] },
    });
    const { container } = renderAt('/events/e1');
    await waitFor(() => expect(screen.getByText('PB Live')).toBeInTheDocument());
    expect(dateLine(container)).toBe('Monday, June 15, 2026 at 1:00 PM');
  });

  it('shows the start–end range when the event has an ends_at', async () => {
    (calApi.getEvent as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      id: 'e1',
      title: 'PB Live',
      starts_at: '2026-06-15T20:00:00Z',
      ends_at: '2026-06-15T23:30:00Z',
      venue: { name: 'The Bowl' },
      score: 0,
      matched_because: { performers: [], genres: [] },
    });
    const { container } = renderAt('/events/e1');
    await waitFor(() => expect(screen.getByText('PB Live')).toBeInTheDocument());
    expect(dateLine(container)).toBe('Monday, June 15, 2026, 1:00 – 4:30 PM');
  });

  it('renders the event detail', async () => {
    (calApi.getEvent as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      id: 'e1',
      title: 'PB Live',
      description: 'indie rock concert',
      starts_at: '2026-06-15T20:00:00Z',
      venue: { name: 'The Bowl', address: '100 Main St' },
      url: 'https://tix.example/aaa',
      score: 0.82,
      matched_because: { performers: ['Phoebe Bridgers'], genres: ['indie'] },
    });
    renderAt('/events/e1');
    await waitFor(() => expect(screen.getByText('PB Live')).toBeInTheDocument());
    expect(screen.getByText(/The Bowl/)).toBeInTheDocument();
    expect(screen.getByText(/100 Main St/)).toBeInTheDocument();
    expect(screen.getByText(/82% match/)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /tickets|view event/i })).toHaveAttribute(
      'href',
      'https://tix.example/aaa',
    );
  });

  it('renders the image tile inside the header when the event has an image_url', async () => {
    (calApi.getEvent as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      id: 'e1',
      title: 'PB Live',
      starts_at: '2026-06-15T20:00:00Z',
      image_url: 'https://cdn.test/pb.jpg',
      venue: { name: 'The Bowl' },
      score: 0.82,
      matched_because: { performers: [], genres: [] },
    });
    const { container } = renderAt('/events/e1');
    await waitFor(() => expect(screen.getByText('PB Live')).toBeInTheDocument());
    const img = container.querySelector(`.${s.detail} img`);
    expect(img).toHaveAttribute('src', 'https://cdn.test/pb.jpg');
  });

  it('renders 404 if event not found', async () => {
    const err = Object.assign(new Error('not found'), {
      status: 404,
      code: 'not_found',
    });
    (calApi.getEvent as ReturnType<typeof vi.fn>).mockRejectedValueOnce(err);
    renderAt('/events/missing');
    await waitFor(() => expect(screen.getByText(/event not found/i)).toBeInTheDocument());
  });

  it('shows the match score for a matched event', async () => {
    (calApi.getEvent as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      id: 'e1',
      title: 'PB Live',
      starts_at: '2026-06-15T20:00:00Z',
      venue: { name: 'The Bowl' },
      score: 0.82,
      matched_because: { performers: ['Phoebe Bridgers'], genres: ['indie'] },
    });
    renderAt('/events/e1');
    await waitFor(() => expect(screen.getByText('PB Live')).toBeInTheDocument());
    expect(screen.getByText(/82% match/)).toBeInTheDocument();
  });

  it('hides the match score for an unmatched city event', async () => {
    (calApi.getEvent as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      id: 'c1',
      title: 'Citywide Show',
      starts_at: '2026-06-15T20:00:00Z',
      venue: { name: 'Civic Hall' },
      score: 0,
      matched_because: { performers: [], genres: [] },
    });
    renderAt('/events/c1');
    await waitFor(() => expect(screen.getByText('Citywide Show')).toBeInTheDocument());
    expect(screen.queryByText(/% match/)).not.toBeInTheDocument();
  });
});

// Everything below `artist` is `omitempty` on the wire, so these fixtures lean
// on the API types to stay honest about what the server can leave out.
type ArtistFixture = NonNullable<CalendarEvent['artist']>;

function renderWithArtist(artist?: ArtistFixture) {
  (calApi.getEvent as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
    id: 'e1',
    title: 'PB Live',
    starts_at: '2026-06-15T20:00:00Z',
    venue: { name: 'The Bowl' },
    score: 0,
    matched_because: { performers: [], genres: [] },
    artist,
  });
  const rendered = renderAt('/events/e1');
  return rendered;
}

const loaded = () => waitFor(() => expect(screen.getByText('PB Live')).toBeInTheDocument());

describe('EventDetailPage artist sections', () => {
  describe('bio', () => {
    it('renders the bio text under an "About the artist" heading', async () => {
      renderWithArtist({ name: 'Phoebe Bridgers', bio: { text: 'Songwriter from LA.' } });
      await loaded();
      expect(screen.getByRole('heading', { name: 'About the artist' })).toBeInTheDocument();
      expect(screen.getByText('Songwriter from LA.')).toBeInTheDocument();
    });

    it('omits the bio section when the artist has no bio', async () => {
      renderWithArtist({ name: 'Phoebe Bridgers' });
      await loaded();
      expect(screen.queryByRole('heading', { name: 'About the artist' })).toBeNull();
    });

    it('omits the bio section when the event has no artist', async () => {
      renderWithArtist(undefined);
      await loaded();
      expect(screen.queryByRole('heading', { name: 'About the artist' })).toBeNull();
    });
  });

  describe('tour', () => {
    const tourArtist = (tour: NonNullable<ArtistFixture['tour']>): ArtistFixture => ({
      name: 'Phoebe Bridgers',
      tour,
    });

    it('renders the tour blurb under a "Tour info" heading', async () => {
      renderWithArtist(tourArtist({ name: 'Reunion Tour', blurb: 'Touring the new record.' }));
      await loaded();
      expect(screen.getByRole('heading', { name: 'Tour info' })).toBeInTheDocument();
      expect(screen.getByText('Touring the new record.')).toBeInTheDocument();
    });

    it('lists the setlist songs in the order the API returned them', async () => {
      renderWithArtist(
        tourArtist({
          blurb: 'Touring the new record.',
          songs: [{ name: 'Motion Sickness' }, { name: 'Kyoto' }, { name: 'Scott Street' }],
        }),
      );
      await loaded();
      expect(screen.getByRole('heading', { name: 'Setlist' })).toBeInTheDocument();
      expect(screen.getAllByRole('listitem').map((li) => li.textContent)).toEqual([
        'Motion Sickness',
        'Kyoto',
        'Scott Street',
      ]);
    });

    it('says where and when the setlist was observed', async () => {
      renderWithArtist(
        tourArtist({
          blurb: 'Touring the new record.',
          songs: [{ name: 'Kyoto' }],
          observed: { date: '2026-05-02', venue: 'Red Rocks', city: 'Morrison' },
        }),
      );
      await loaded();
      expect(
        screen.getByText('Observed on 2026-05-02 at Red Rocks in Morrison'),
      ).toBeInTheDocument();
    });

    it('leaves the city out of the observed line when the API omits it', async () => {
      renderWithArtist(
        tourArtist({
          blurb: 'Touring the new record.',
          songs: [{ name: 'Kyoto' }],
          observed: { date: '2026-05-02', venue: 'Red Rocks' },
        }),
      );
      await loaded();
      expect(screen.getByText('Observed on 2026-05-02 at Red Rocks')).toBeInTheDocument();
    });

    it('omits the observed line when the tour has no observed setlist', async () => {
      const { container } = renderWithArtist(
        tourArtist({ blurb: 'Touring the new record.', songs: [{ name: 'Kyoto' }] }),
      );
      await loaded();
      expect(screen.getByRole('heading', { name: 'Setlist' })).toBeInTheDocument();
      expect(container.querySelector(`.${s.setlistObserved}`)).toBeNull();
      expect(screen.queryByText(/Observed on/)).toBeNull();
    });

    it('links out to setlist.fm', async () => {
      renderWithArtist(
        tourArtist({
          blurb: 'Touring the new record.',
          setlist_url: 'https://setlist.fm/pb/2026',
          songs: [{ name: 'Kyoto' }],
        }),
      );
      await loaded();
      expect(screen.getByRole('link', { name: /view on setlist\.fm/i })).toHaveAttribute(
        'href',
        'https://setlist.fm/pb/2026',
      );
    });

    it('shows the setlist link for a tour with no songs', async () => {
      renderWithArtist(
        tourArtist({
          blurb: 'Touring the new record.',
          setlist_url: 'https://setlist.fm/pb/2026',
          songs: [],
        }),
      );
      await loaded();
      expect(screen.getByRole('heading', { name: 'Setlist' })).toBeInTheDocument();
      expect(screen.queryByRole('list')).toBeNull();
      expect(screen.getByRole('link', { name: /view on setlist\.fm/i })).toBeInTheDocument();
    });

    it('lists the songs for a tour with no setlist link', async () => {
      renderWithArtist(
        tourArtist({ blurb: 'Touring the new record.', songs: [{ name: 'Kyoto' }] }),
      );
      await loaded();
      expect(screen.getAllByRole('listitem').map((li) => li.textContent)).toEqual(['Kyoto']);
      expect(screen.queryByRole('link', { name: /setlist\.fm/i })).toBeNull();
    });

    it('omits the setlist when the tour has neither songs nor a setlist link', async () => {
      renderWithArtist(tourArtist({ blurb: 'Touring the new record.', songs: [] }));
      await loaded();
      expect(screen.getByText('Touring the new record.')).toBeInTheDocument();
      expect(screen.queryByRole('heading', { name: 'Setlist' })).toBeNull();
      expect(screen.queryByRole('list')).toBeNull();
      expect(screen.queryByRole('link', { name: /setlist\.fm/i })).toBeNull();
    });

    it('omits the tour section when the artist has no tour', async () => {
      renderWithArtist({ name: 'Phoebe Bridgers', bio: { text: 'Songwriter from LA.' } });
      await loaded();
      expect(screen.queryByRole('heading', { name: 'Tour info' })).toBeNull();
    });
  });
});
