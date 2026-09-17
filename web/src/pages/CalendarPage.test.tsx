import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
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
  listNotInterested: vi.fn(),
}));

// Every card reads the going list to colour its toggle, and the sidebar reads
// it again as whole events. Unmocked, both reach for the network.
vi.mock('../api/eventGoing', () => ({
  listGoing: vi.fn(),
  listGoingEvents: vi.fn(),
  markEventGoing: vi.fn(),
  resetEventGoing: vi.fn(),
}));

vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));

vi.mock('../api/spotify', () => ({
  getSpotifyStatus: vi.fn(),
  startSpotifyConnect: vi.fn(),
}));

vi.mock('../api/manualInterests', () => ({
  listManualInterests: vi.fn(),
}));

// Without this SearchDialog (mounted by CalendarPage) reaches a real fetch.
vi.mock('../api/search', () => ({ searchEvents: vi.fn() }));

import * as calApi from '../api/calendar';
import * as niApi from '../api/notInterested';
import { listGoing, listGoingEvents } from '../api/eventGoing';
import { useAuth } from '../auth/useAuth';
import { getSpotifyStatus } from '../api/spotify';
import { listManualInterests } from '../api/manualInterests';
import { searchEvents } from '../api/search';

function renderPage(
  qc: QueryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } }),
) {
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
  // resetAllMocks drops the factory's implementations, so the empty going list
  // is restored here — a queryFn resolving to undefined is a react-query error.
  vi.mocked(listGoing).mockResolvedValue([]);
  vi.mocked(listGoingEvents).mockResolvedValue([]);
  vi.mocked(niApi.listNotInterested).mockResolvedValue([]);
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
    user: {
      id: 'u1',
      email: 'u@example.com',
      city_id: 'city-1',
      confirmed: true,
      show_setlists: false,
    },
    login: vi.fn(),
    signup: vi.fn(),
    logout: vi.fn(),
    refreshUser: vi.fn(),
    deleteAccount: vi.fn(),
  });
});

describe('CalendarPage', () => {
  it('renders matched events', async () => {
    (calApi.getCalendar as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      events: [
        {
          id: 'e1',
          title: 'PB Live',
          starts_at: '2026-06-15T20:00:00Z',
          venue: { name: 'The Bowl' },
          score: 0.82,
          matched_because: { performers: ['Phoebe Bridgers'], genres: ['indie'] },
        },
      ],
    });
    renderPage();
    await waitFor(() => expect(screen.getByText('PB Live')).toBeInTheDocument());
    expect(screen.getByText(/82% match/)).toBeInTheDocument();
    expect(screen.getByText(/The Bowl/)).toBeInTheDocument();
    expect(screen.getByText(/Phoebe Bridgers, indie/)).toBeInTheDocument();
  });

  // The Full | Condensed display-style toggle is commented out in
  // CalendarPage for now, so there are no buttons for this to press.
  // it('persists the selected display style across remounts via localStorage', async () => {
  //   (calApi.getCalendar as ReturnType<typeof vi.fn>).mockResolvedValue({ events: [] });
  //
  //   const first = renderPage();
  //   // Defaults to Full when nothing has been persisted yet.
  //   expect(screen.getByRole('button', { name: 'Full' })).toHaveAttribute('aria-pressed', 'true');
  //   expect(screen.getByRole('button', { name: 'Condensed' })).toHaveAttribute(
  //     'aria-pressed',
  //     'false',
  //   );
  //
  //   fireEvent.click(screen.getByRole('button', { name: 'Condensed' }));
  //   expect(screen.getByRole('button', { name: 'Condensed' })).toHaveAttribute(
  //     'aria-pressed',
  //     'true',
  //   );
  //
  //   first.unmount();
  //
  //   // A fresh mount should remember the choice from localStorage.
  //   renderPage();
  //   expect(screen.getByRole('button', { name: 'Condensed' })).toHaveAttribute(
  //     'aria-pressed',
  //     'true',
  //   );
  //   expect(screen.getByRole('button', { name: 'Full' })).toHaveAttribute('aria-pressed', 'false');
  // });

  it('shows empty state when there are no matches', async () => {
    (calApi.getCalendar as ReturnType<typeof vi.fn>).mockResolvedValueOnce({ events: [] });
    renderPage();
    await waitFor(() => expect(screen.getByText(/no upcoming matches yet/i)).toBeInTheDocument());
  });

  it('requests the first page without a cursor', async () => {
    const getCal = calApi.getCalendar as ReturnType<typeof vi.fn>;
    getCal.mockResolvedValue({ events: [] });
    renderPage();

    await waitFor(() => expect(getCal).toHaveBeenCalled());
    expect(getCal).toHaveBeenCalledWith(undefined);
  });

  // The calendar endpoint no longer filters dismissals out server-side, so the
  // card has to stay gone on the strength of the not-interested list alone —
  // hence a refetch here that keeps returning the event.
  it('removes a card and calls the API when Hide is clicked', async () => {
    let hidden: string[] = [];
    vi.mocked(niApi.listNotInterested).mockImplementation(async () => hidden);
    vi.mocked(niApi.markNotInterested).mockImplementation(async (id: string) => {
      hidden = [id];
    });
    vi.mocked(calApi.getCalendar).mockResolvedValue({
      events: [
        {
          id: 'e1',
          title: 'PB Live',
          starts_at: '2026-06-15T20:00:00Z',
          venue: { name: 'The Bowl' },
          score: 0.82,
          matched_because: { performers: [], genres: [] },
        },
      ],
    });

    renderPage();
    await waitFor(() => expect(screen.getByText('PB Live')).toBeInTheDocument());

    fireEvent.click(screen.getByRole('button', { name: /hide/i }));

    await waitFor(() => expect(niApi.markNotInterested).toHaveBeenCalledWith('e1'));
    await waitFor(() => expect(screen.queryByText('PB Live')).not.toBeInTheDocument());
  });
});

describe('CalendarPage search', () => {
  it('opens the search dialog from the Search button', async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole('button', { name: /search/i }));
    expect(screen.getByRole('dialog', { name: /search all seattle events/i })).toBeInTheDocument();
  });

  // Regression: CalendarPage registers a bare-'c' window shortcut. Without a
  // focus guard, every 'c' typed into any field on the page toggles the
  // all-city calendar -- so searching for "comedy" silently swaps the
  // calendar behind the dialog. "comedy" has exactly one 'c': a query with an
  // even number of them (e.g. "crocodile") would round-trip the toggle back
  // to its starting value and pass against the unguarded handler too, so
  // don't swap this for a word chosen on vibes alone.
  it('does not toggle the all-city calendar when a c is typed into the search box', async () => {
    vi.mocked(searchEvents).mockResolvedValue({ results: [] });
    const user = userEvent.setup();
    renderPage();
    const headingBefore = screen.getByRole('heading', { level: 1 }).textContent;

    await user.click(screen.getByRole('button', { name: /search/i }));
    await user.type(screen.getByRole('combobox'), 'comedy');

    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(headingBefore!);
  });

  // The dialog fires no request when its query is disabled (see useEventSearch),
  // so an undefined cityId doesn't read as an error -- it silently never
  // fetches and the dialog falls through to "No events match". That reads to
  // the user as a definitive empty search result for a search that never ran.
  // The button must stay disabled until user.city_id is known so that failure
  // mode is unreachable, rather than papering over it inside the dialog.
  it('does not enable the Search button until the city is known', async () => {
    vi.mocked(useAuth).mockReturnValue({
      status: 'loading',
      user: null,
      login: vi.fn(),
      signup: vi.fn(),
      logout: vi.fn(),
      refreshUser: vi.fn(),
      deleteAccount: vi.fn(),
    });
    vi.mocked(calApi.getCalendar).mockResolvedValue({ events: [] });

    const user = userEvent.setup();
    renderPage();

    const button = await screen.findByRole('button', { name: /search/i });
    expect(button).toBeDisabled();

    // Belt-and-braces: even attempting the click must not open the dialog or
    // reach the search API.
    await user.click(button);
    expect(
      screen.queryByRole('dialog', { name: /search all seattle events/i }),
    ).not.toBeInTheDocument();
    expect(searchEvents).not.toHaveBeenCalled();
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
    vi.mocked(calApi.getCalendar).mockResolvedValue({ events: [] });
    vi.mocked(calApi.getCityCalendar).mockResolvedValue({ events: [cityEvent] });

    renderPage();

    await waitFor(() => expect(screen.getByText('Citywide Show')).toBeInTheDocument());
    expect(calApi.getCityCalendar).toHaveBeenCalledWith('city-1', undefined);
    expect(screen.getByText("What's happening in Seattle")).toBeInTheDocument();
    expect(screen.getByText(/Showing all events in Seattle/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Hide' })).not.toBeInTheDocument();
    expect(screen.queryByText(/% match/)).not.toBeInTheDocument();
  });

  it('shows the matched calendar when Spotify is connected', async () => {
    vi.mocked(getSpotifyStatus).mockResolvedValue({ connected: true });
    vi.mocked(listManualInterests).mockResolvedValue([]);
    vi.mocked(calApi.getCalendar).mockResolvedValue({ events: [] });

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
    vi.mocked(calApi.getCalendar).mockResolvedValue({ events: [] });

    renderPage();

    await waitFor(() => expect(screen.getByText(/no upcoming matches yet/i)).toBeInTheDocument());
    expect(calApi.getCityCalendar).not.toHaveBeenCalled();
  });

  // A failed gate query must degrade to today's behavior, not to a stuck
  // spinner and not to a city-wide list.
  it('shows the matched calendar when the status query fails', async () => {
    vi.mocked(getSpotifyStatus).mockRejectedValue(new Error('boom'));
    vi.mocked(listManualInterests).mockResolvedValue([]);
    vi.mocked(calApi.getCalendar).mockResolvedValue({ events: [] });

    renderPage();

    await waitFor(() => expect(screen.getByText(/no upcoming matches yet/i)).toBeInTheDocument());
    expect(calApi.getCityCalendar).not.toHaveBeenCalled();
  });

  it('tells the user when the city has no events at all', async () => {
    noInterests();
    vi.mocked(calApi.getCalendar).mockResolvedValue({ events: [] });
    vi.mocked(calApi.getCityCalendar).mockResolvedValue({ events: [] });

    renderPage();

    await waitFor(() =>
      expect(screen.getByText(/nothing on the calendar in seattle/i)).toBeInTheDocument(),
    );
  });

  it('shows the matched calendar when the interests query fails', async () => {
    vi.mocked(getSpotifyStatus).mockResolvedValue({ connected: false });
    vi.mocked(listManualInterests).mockRejectedValue(new Error('boom'));
    vi.mocked(calApi.getCalendar).mockResolvedValue({ events: [] });

    renderPage();

    await waitFor(() => expect(screen.getByText(/no upcoming matches yet/i)).toBeInTheDocument());
    expect(calApi.getCityCalendar).not.toHaveBeenCalled();
  });

  it('shows an error box when the city calendar fails to load', async () => {
    noInterests();
    vi.mocked(calApi.getCalendar).mockResolvedValue({ events: [] });
    vi.mocked(calApi.getCityCalendar).mockRejectedValue(new Error('boom'));

    renderPage();

    await waitFor(() =>
      expect(screen.getByText(/couldn't load your calendar/i)).toBeInTheDocument(),
    );
  });

  it('leaves fallback mode once an added interest invalidates the interests query', async () => {
    noInterests();
    vi.mocked(calApi.getCalendar).mockResolvedValue({ events: [] });
    vi.mocked(calApi.getCityCalendar).mockResolvedValue({ events: [cityEvent] });

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    renderPage(qc);

    await waitFor(() =>
      expect(screen.getByText("What's happening in Seattle")).toBeInTheDocument(),
    );

    vi.mocked(listManualInterests).mockResolvedValue([
      {
        id: 'i1',
        value: 'indie',
        normalized_value: 'indie',
        weight: 1,
        created_at: '2026-01-01T00:00:00Z',
      },
    ]);
    // The same invalidation InterestsPage's add/remove mutations perform
    // (InterestsPage.tsx:54, :62) on success.
    qc.invalidateQueries({ queryKey: ['manual-interests'] });

    await waitFor(() => expect(screen.getByText('Your Seattle calendar')).toBeInTheDocument());
  });
});
