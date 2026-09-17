import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import MyShowsPage from './MyShowsPage';
import type { CalendarEvent } from '../api/calendar';

// The page is the going list at full size, which reads both the scraped going
// events and the hand-added ones. Unmocked, both reach for the network.
vi.mock('../api/eventGoing', () => ({
  listGoing: vi.fn(),
  listGoingEvents: vi.fn(),
  markEventGoing: vi.fn(),
  resetEventGoing: vi.fn(),
}));
vi.mock('../api/manualGoingEvents', () => ({
  listManualGoingEvents: vi.fn(),
  createManualGoingEvent: vi.fn(),
  updateManualGoingEvent: vi.fn(),
  deleteManualGoingEvent: vi.fn(),
}));
vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));

import { listGoingEvents } from '../api/eventGoing';
import { listManualGoingEvents } from '../api/manualGoingEvents';
import { useAuth } from '../auth/useAuth';

const upcoming: CalendarEvent = {
  id: 'e1',
  title: 'PB Live',
  starts_at: '2026-06-15T20:00:00Z',
  venue: { name: 'The Bowl' },
  score: 0.82,
  matched_because: { performers: ['Phoebe Bridgers'], genres: ['indie'] },
};

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/my-shows']}>
        <Routes>
          <Route path="/my-shows" element={<MyShowsPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  // The Past Shows section remembers whether it was open in localStorage.
  localStorage.clear();
  vi.setSystemTime(new Date('2026-06-01T00:00:00Z'));
  // resetAllMocks drops the factory's implementations, so the empty lists are
  // restored here -- a queryFn resolving to undefined is a react-query error.
  vi.mocked(listGoingEvents).mockResolvedValue([]);
  vi.mocked(listManualGoingEvents).mockResolvedValue([]);
  vi.mocked(useAuth).mockReturnValue({
    status: 'authenticated',
    user: { id: 'u1', email: 'a@x', city_id: 'city-1', confirmed: true, show_setlists: false },
    login: vi.fn(),
    signup: vi.fn(),
    logout: vi.fn(),
    refreshUser: vi.fn(),
    deleteAccount: vi.fn(),
  });
});

afterEach(() => {
  vi.useRealTimers();
});

describe('MyShowsPage', () => {
  // On the calendar the list is a sidebar section (h2); here it is the page.
  it('titles the page with the upcoming shows heading and an Add button', () => {
    renderPage();
    expect(screen.getByRole('heading', { level: 1, name: 'Upcoming Shows' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add a show by hand' })).toBeInTheDocument();
  });

  it('lists the shows the user is going to', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([upcoming]);
    vi.mocked(listManualGoingEvents).mockResolvedValue([
      {
        id: 'm1',
        date: '2026-08-20',
        event_name: 'Manual Future Show',
        venue_name: 'Manual Venue',
        created_at: '2026-01-01T00:00:00Z',
      },
    ]);
    renderPage();

    expect(await screen.findByText('PB Live')).toBeInTheDocument();
    expect(screen.getByText('The Bowl')).toBeInTheDocument();
    expect(screen.getByText('Manual Future Show')).toBeInTheDocument();
  });

  it('shows the empty state when the user is going to nothing', async () => {
    renderPage();
    expect(await screen.findByText(/No upcoming shows yet/)).toBeInTheDocument();
  });
});
