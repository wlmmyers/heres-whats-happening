import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import type { CalendarEvent } from '../api/calendar';
import type { Venue } from '../api/venues';

// The page renders Layout itself, and Layout reads the auth context.
vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));
vi.mock('../api/venues', () => ({ getCityVenues: vi.fn() }));
vi.mock('../api/calendar', () => ({ getCityCalendar: vi.fn() }));
// EventCard reads the going list to colour its toggle.
vi.mock('../api/eventGoing', () => ({
  listGoing: vi.fn(),
  markEventGoing: vi.fn(),
  resetEventGoing: vi.fn(),
}));

import { useAuth } from '../auth/useAuth';
import { getCityVenues } from '../api/venues';
import { getCityCalendar } from '../api/calendar';
import { listGoing } from '../api/eventGoing';
import VenuesPage from './VenuesPage';

// happy-dom has no IntersectionObserver; LazyList only needs one to exist.
class NoopIntersectionObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

function renderPage(path = '/venues') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <VenuesPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

const venues: Venue[] = [
  {
    id: 'v1',
    name: 'The Crocodile',
    address: '2505 1st Ave, Seattle, WA',
    lat: 47.6,
    lng: -122.3,
    website_url: 'https://www.thecrocodile.com/events',
    upcoming_event_count: 12,
    next_event_at: '2026-10-18T03:00:00Z',
  },
  {
    id: 'v2',
    name: 'Quiet Hall',
    upcoming_event_count: 3,
    next_event_at: '2026-10-20T03:00:00Z',
  },
];

function event(id: string, title: string): CalendarEvent {
  return {
    id,
    title,
    starts_at: '2026-10-18T03:00:00Z',
    venue: { name: 'The Crocodile' },
    score: 0,
    matched_because: { performers: [], genres: [] },
  };
}

const selector = () => screen.getByLabelText('Venues', { selector: 'div' });

beforeEach(() => {
  vi.resetAllMocks();
  vi.stubGlobal('IntersectionObserver', NoopIntersectionObserver);
  vi.mocked(listGoing).mockResolvedValue([]);
  vi.mocked(getCityVenues).mockResolvedValue({ venues });
  vi.mocked(getCityCalendar).mockImplementation(async (_city, _cursor, venueId) => ({
    events: venueId === 'v1' ? [event('e1', 'Croc Show'), event('e2', 'Croc Late Show')] : [],
  }));
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
  vi.unstubAllGlobals();
});

describe('VenuesPage', () => {
  it("lists every venue in the user's city in the selector", async () => {
    renderPage();
    await screen.findByRole('heading', { name: 'The Crocodile' });
    expect(getCityVenues).toHaveBeenCalledWith('city-1');
    const buttons = within(selector()).getAllByRole('button');
    expect(buttons.map((b) => b.textContent)).toEqual(['The Crocodile12', 'Quiet Hall3']);
  });

  it('selects the first venue by default and shows its detail and events', async () => {
    renderPage();
    expect(await screen.findByRole('heading', { name: 'The Crocodile' })).toBeInTheDocument();
    expect(within(selector()).getByRole('button', { name: /the crocodile/i })).toHaveAttribute(
      'aria-pressed',
      'true',
    );
    expect(screen.getByText('2505 1st Ave, Seattle, WA')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /map/i })).toHaveAttribute(
      'href',
      expect.stringContaining('google.com/maps'),
    );
    expect(screen.getByRole('link', { name: /thecrocodile\.com/ })).toHaveAttribute(
      'href',
      'https://www.thecrocodile.com/events',
    );
    expect(screen.getByText(/^Next: /)).toBeInTheDocument();

    expect(await screen.findByText('Croc Show')).toBeInTheDocument();
    expect(screen.getByText('Croc Late Show')).toBeInTheDocument();
    expect(getCityCalendar).toHaveBeenCalledWith('city-1', undefined, 'v1');
  });

  it('switches the detail and events when another venue is selected', async () => {
    renderPage();
    await screen.findByText('Croc Show');

    await userEvent.click(within(selector()).getByRole('button', { name: /quiet hall/i }));

    expect(await screen.findByRole('heading', { name: 'Quiet Hall' })).toBeInTheDocument();
    expect(await screen.findByText('No upcoming shows at Quiet Hall.')).toBeInTheDocument();
    expect(screen.queryByText('Croc Show')).not.toBeInTheDocument();
    expect(getCityCalendar).toHaveBeenCalledWith('city-1', undefined, 'v2');
  });

  it('restores the selected venue from the URL', async () => {
    renderPage('/venues?venue=v2');
    expect(await screen.findByRole('heading', { name: 'Quiet Hall' })).toBeInTheDocument();
  });

  it('falls back to the first venue for an unknown id', async () => {
    renderPage('/venues?venue=nope');
    expect(await screen.findByRole('heading', { name: 'The Crocodile' })).toBeInTheDocument();
  });

  it('shows an empty state when the city has no venues', async () => {
    vi.mocked(getCityVenues).mockResolvedValue({ venues: [] });
    renderPage();
    expect(
      await screen.findByText('No Seattle venues have upcoming shows right now.'),
    ).toBeInTheDocument();
  });

  it('shows an error when venues fail to load', async () => {
    vi.mocked(getCityVenues).mockRejectedValue(new Error('boom'));
    renderPage();
    expect(await screen.findByText(/couldn't load venues/i)).toBeInTheDocument();
  });

  it("shows an error when a venue's events fail to load", async () => {
    vi.mocked(getCityCalendar).mockRejectedValue(new Error('boom'));
    renderPage();
    expect(await screen.findByText(/couldn't load shows at the crocodile/i)).toBeInTheDocument();
  });
});
