import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import CalendarPage from './CalendarPage';

vi.mock('../api/calendar', () => ({
  getCalendar: vi.fn(),
  getCityCalendar: vi.fn(),
  getEvent: vi.fn(),
}));

vi.mock('../api/notInterested', () => ({
  markNotInterested: vi.fn(),
  resetNotInterested: vi.fn(),
}));

vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));

vi.mock('../api/spotify', () => ({
  getSpotifyStatus: vi.fn(),
  startSpotifyConnect: vi.fn(),
}));

vi.mock('../api/manualInterests', () => ({
  listManualInterests: vi.fn(),
}));

import * as calApi from '../api/calendar';
import * as niApi from '../api/notInterested';
import { useAuth } from '../auth/useAuth';
import { getSpotifyStatus } from '../api/spotify';
import { listManualInterests } from '../api/manualInterests';

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/calendar/seattle']}>
        <Routes>
          <Route path="/calendar/seattle" element={<CalendarPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  localStorage.clear();
  vi.mocked(getSpotifyStatus).mockResolvedValue({ connected: true });
  vi.mocked(listManualInterests).mockResolvedValue([
    {
      id: 'i1',
      value: 'indie',
      normalized_value: 'indie',
      weight: 1,
      created_at: '2026-01-01T00:00:00Z',
    },
  ]);
  vi.mocked(useAuth).mockReturnValue({
    status: 'authenticated',
    user: { id: 'u1', email: 'u@example.com', city_id: 'city-1', confirmed: true },
    login: vi.fn(),
    signup: vi.fn(),
    logout: vi.fn(),
    refreshUser: vi.fn(),
  });
});

describe('CalendarPage', () => {
  it('renders matched events', async () => {
    (calApi.getCalendar as ReturnType<typeof vi.fn>).mockResolvedValueOnce([
      {
        id: 'e1',
        title: 'PB Live',
        starts_at: '2026-06-15T20:00:00Z',
        venue: { name: 'The Bowl' },
        score: 0.82,
        matched_because: { performers: ['Phoebe Bridgers'], genres: ['indie'] },
      },
    ]);
    renderPage();
    await waitFor(() => expect(screen.getByText('PB Live')).toBeInTheDocument());
    expect(screen.getByText(/82% match/)).toBeInTheDocument();
    expect(screen.getByText(/The Bowl/)).toBeInTheDocument();
    expect(screen.getByText(/Phoebe Bridgers, indie/)).toBeInTheDocument();
  });

  it('persists the selected display style across remounts via localStorage', async () => {
    (calApi.getCalendar as ReturnType<typeof vi.fn>).mockResolvedValue([]);

    const first = renderPage();
    // Defaults to Full when nothing has been persisted yet.
    expect(screen.getByRole('button', { name: 'Full' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: 'Condensed' })).toHaveAttribute(
      'aria-pressed',
      'false',
    );

    fireEvent.click(screen.getByRole('button', { name: 'Condensed' }));
    expect(screen.getByRole('button', { name: 'Condensed' })).toHaveAttribute(
      'aria-pressed',
      'true',
    );

    first.unmount();

    // A fresh mount should remember the choice from localStorage.
    renderPage();
    expect(screen.getByRole('button', { name: 'Condensed' })).toHaveAttribute(
      'aria-pressed',
      'true',
    );
    expect(screen.getByRole('button', { name: 'Full' })).toHaveAttribute('aria-pressed', 'false');
  });

  it('shows empty state when there are no matches', async () => {
    (calApi.getCalendar as ReturnType<typeof vi.fn>).mockResolvedValueOnce([]);
    renderPage();
    await waitFor(() => expect(screen.getByText(/no upcoming matches yet/i)).toBeInTheDocument());
  });

  it('requests from the local calendar date, not the UTC date', async () => {
    // 5pm Pacific on 7/25 is already 7/26 in UTC. Deriving `from` from the UTC
    // date drops every event left in the local day.
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.setSystemTime(new Date('2026-07-25T17:00:00-07:00'));
    try {
      const getCal = calApi.getCalendar as ReturnType<typeof vi.fn>;
      getCal.mockResolvedValue([]);
      renderPage();

      await waitFor(() => expect(getCal).toHaveBeenCalled());
      expect(getCal.mock.calls[0][0]).toBe('2026-07-25');
    } finally {
      vi.useRealTimers();
    }
  });

  it('removes a card and calls the API when Not interested is clicked', async () => {
    (calApi.getCalendar as ReturnType<typeof vi.fn>)
      .mockResolvedValueOnce([
        {
          id: 'e1',
          title: 'PB Live',
          starts_at: '2026-06-15T20:00:00Z',
          venue: { name: 'The Bowl' },
          score: 0.82,
          matched_because: { performers: [], genres: [] },
        },
      ])
      .mockResolvedValue([]); // refetch after dismissal returns the server-filtered list
    (niApi.markNotInterested as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);

    renderPage();
    await waitFor(() => expect(screen.getByText('PB Live')).toBeInTheDocument());

    fireEvent.click(screen.getByRole('button', { name: /not interested/i }));

    await waitFor(() => expect(niApi.markNotInterested).toHaveBeenCalledWith('e1'));
    await waitFor(() => expect(screen.queryByText('PB Live')).not.toBeInTheDocument());
  });
});

describe('CalendarPage city fallback', () => {
  const cityEvent = {
    id: 'c1',
    title: 'Citywide Show',
    starts_at: '2026-06-15T20:00:00Z',
    venue: { name: 'Civic Hall' },
    score: 0,
    matched_because: { performers: [], genres: [] },
  };

  function noInterests() {
    vi.mocked(getSpotifyStatus).mockResolvedValue({ connected: false });
    vi.mocked(listManualInterests).mockResolvedValue([]);
  }

  it('shows every city event when Spotify is disconnected and there are no interests', async () => {
    noInterests();
    vi.mocked(calApi.getCalendar).mockResolvedValue([]);
    vi.mocked(calApi.getCityCalendar).mockResolvedValue([cityEvent]);

    renderPage();

    await waitFor(() => expect(screen.getByText('Citywide Show')).toBeInTheDocument());
    expect(calApi.getCityCalendar).toHaveBeenCalledWith(
      'city-1',
      expect.any(String),
      expect.any(String),
    );
    expect(screen.getByText('Everything happening in Seattle')).toBeInTheDocument();
    expect(screen.getByText(/Showing everything in Seattle/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Not interested' })).not.toBeInTheDocument();
    expect(screen.queryByText(/% match/)).not.toBeInTheDocument();
  });

  it('shows the matched calendar when Spotify is connected', async () => {
    vi.mocked(getSpotifyStatus).mockResolvedValue({ connected: true });
    vi.mocked(listManualInterests).mockResolvedValue([]);
    vi.mocked(calApi.getCalendar).mockResolvedValue([]);

    renderPage();

    await waitFor(() => expect(screen.getByText(/no upcoming matches yet/i)).toBeInTheDocument());
    expect(calApi.getCityCalendar).not.toHaveBeenCalled();
  });

  it('shows the matched calendar when the user has manual interests', async () => {
    vi.mocked(getSpotifyStatus).mockResolvedValue({ connected: false });
    vi.mocked(listManualInterests).mockResolvedValue([
      {
        id: 'i1',
        value: 'indie',
        normalized_value: 'indie',
        weight: 1,
        created_at: '2026-01-01T00:00:00Z',
      },
    ]);
    vi.mocked(calApi.getCalendar).mockResolvedValue([]);

    renderPage();

    await waitFor(() => expect(screen.getByText(/no upcoming matches yet/i)).toBeInTheDocument());
    expect(calApi.getCityCalendar).not.toHaveBeenCalled();
  });

  // A failed gate query must degrade to today's behavior, not to a stuck
  // spinner and not to a city-wide list.
  it('shows the matched calendar when the status query fails', async () => {
    vi.mocked(getSpotifyStatus).mockRejectedValue(new Error('boom'));
    vi.mocked(listManualInterests).mockResolvedValue([]);
    vi.mocked(calApi.getCalendar).mockResolvedValue([]);

    renderPage();

    await waitFor(() => expect(screen.getByText(/no upcoming matches yet/i)).toBeInTheDocument());
    expect(calApi.getCityCalendar).not.toHaveBeenCalled();
  });

  it('tells the user when the city has no events at all', async () => {
    noInterests();
    vi.mocked(calApi.getCalendar).mockResolvedValue([]);
    vi.mocked(calApi.getCityCalendar).mockResolvedValue([]);

    renderPage();

    await waitFor(() =>
      expect(screen.getByText(/nothing on the calendar in seattle/i)).toBeInTheDocument(),
    );
  });
});
