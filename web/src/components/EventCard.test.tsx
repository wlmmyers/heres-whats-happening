import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import EventCard from './EventCard';
import * as s from './EventCard.css';
import type { CalendarEvent } from '../api/calendar';

// The card reads the going list to colour its toggle, which pulls in the query
// client and the signed-in user.
vi.mock('../api/eventGoing', () => ({
  listGoing: vi.fn(),
  markEventGoing: vi.fn(),
  resetEventGoing: vi.fn(),
}));

vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));

import { listGoing, markEventGoing, resetEventGoing } from '../api/eventGoing';
import { useAuth } from '../auth/useAuth';

const event: CalendarEvent = {
  id: 'e1',
  title: 'PB Live',
  starts_at: '2026-06-15T20:00:00Z',
  venue: { name: 'The Bowl' },
  score: 0.82,
  matched_because: { performers: ['Phoebe Bridgers'], genres: ['indie'] },
};

function withProviders(children: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

function renderCard(onNotInterested?: (id: string) => void, overrides?: Partial<CalendarEvent>) {
  return render(
    withProviders(
      <MemoryRouter>
        <EventCard event={{ ...event, ...overrides }} onNotInterested={onNotInterested} />
      </MemoryRouter>,
    ),
  );
}

// ICU pads its range patterns with thin and narrow no-break spaces; compare
// against plain spaces so the expectations stay readable.
function dateLine(container: HTMLElement) {
  return container.querySelector(`.${s.date}`)?.textContent?.replace(/[\u2009\u202f\u00a0]/g, ' ');
}

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(listGoing).mockResolvedValue([]);
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
  });
});

afterEach(() => {
  vi.useRealTimers();
});

describe('EventCard', () => {
  it('shows only the start time when the event has no ends_at', () => {
    vi.setSystemTime(new Date('2026-06-01T00:00:00Z'));
    const { container } = renderCard();
    expect(dateLine(container)).toBe('Mon, Jun 15, 1:00 PM · The Bowl');
  });

  it('shows the start–end range when the event has an ends_at', () => {
    vi.setSystemTime(new Date('2026-06-01T00:00:00Z'));
    const { container } = renderCard(undefined, { ends_at: '2026-06-15T23:30:00Z' });
    expect(dateLine(container)).toBe('Mon, Jun 15, 1:00 – 4:30 PM · The Bowl');
  });

  it('navigates to the event detail page when clicked', () => {
    render(
      withProviders(
        <MemoryRouter initialEntries={['/']}>
          <Routes>
            <Route path="/" element={<EventCard event={event} />} />
            <Route path="/events/:id" element={<div>Event detail page</div>} />
          </Routes>
        </MemoryRouter>,
      ),
    );
    fireEvent.click(screen.getByRole('heading', { name: 'PB Live' }));
    expect(screen.getByText('Event detail page')).toBeInTheDocument();
  });

  it('renders an image tile when the event has an image_url', () => {
    const { container } = renderCard(undefined, { image_url: 'https://cdn.test/pb.jpg' });
    expect(container.querySelector('img')).toHaveAttribute('src', 'https://cdn.test/pb.jpg');
  });

  it('renders no image tile when the event has no image_url', () => {
    const { container } = renderCard();
    expect(container.querySelector('img')).toBeNull();
  });

  it('renders no Hide button without the callback', () => {
    renderCard();
    expect(screen.queryByRole('button', { name: /hide/i })).not.toBeInTheDocument();
  });

  it('calls onNotInterested with the event id when clicked', () => {
    const onNotInterested = vi.fn();
    renderCard(onNotInterested);
    fireEvent.click(screen.getByRole('button', { name: /hide/i }));
    expect(onNotInterested).toHaveBeenCalledWith('e1');
  });

  it('renders no link when not interactive', () => {
    render(
      withProviders(
        <MemoryRouter>
          <EventCard event={event} interactive={false} />
        </MemoryRouter>,
      ),
    );
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
    expect(screen.getByText('PB Live')).toBeInTheDocument();
  });

  it('shows the match score for a matched event', () => {
    renderCard();
    expect(screen.getByText(/82% match/)).toBeInTheDocument();
  });

  it('hides the match score for an unmatched city event', () => {
    renderCard(undefined, { score: 0, matched_because: { performers: [], genres: [] } });
    expect(screen.queryByText(/% match/)).not.toBeInTheDocument();
  });
});

describe('EventCard going toggle', () => {
  // Stands in for the server's going list. The card mirrors that list into its
  // own state on every render, so a mock that always answered the same way
  // would undo the click it just recorded on the next refetch.
  function stubGoingList(initial: string[] = []) {
    let going = [...initial];
    vi.mocked(listGoing).mockImplementation(async () => going);
    vi.mocked(markEventGoing).mockImplementation(async (id: string) => {
      going = [...going, id];
    });
    vi.mocked(resetEventGoing).mockImplementation(async (id: string) => {
      going = going.filter((e) => e !== id);
    });
  }

  function renderRouted(onNotInterested: (id: string) => void = vi.fn()) {
    return render(
      withProviders(
        <MemoryRouter initialEntries={['/']}>
          <Routes>
            <Route
              path="/"
              element={<EventCard event={event} onNotInterested={onNotInterested} />}
            />
            <Route path="/events/:id" element={<div>Event detail page</div>} />
          </Routes>
        </MemoryRouter>,
      ),
    );
  }

  it('invites the user to mark an event they are not going to', async () => {
    stubGoingList();
    renderCard(vi.fn());
    expect(await screen.findByRole('button', { name: "I'm going!" })).toBeInTheDocument();
  });

  it('shows an event already on the going list as going', async () => {
    stubGoingList(['e1']);
    renderCard(vi.fn());
    expect(await screen.findByRole('button', { name: 'Going' })).toBeInTheDocument();
  });

  // Another card's event on the list must not colour this one's toggle.
  it('ignores a going list that does not name this event', async () => {
    stubGoingList(['some-other-event']);
    renderCard(vi.fn());
    expect(await screen.findByRole('button', { name: "I'm going!" })).toBeInTheDocument();
  });

  it('marks the event going when the toggle is clicked', async () => {
    stubGoingList();
    renderCard(vi.fn());

    fireEvent.click(await screen.findByRole('button', { name: "I'm going!" }));

    await waitFor(() => expect(markEventGoing).toHaveBeenCalledWith('e1'));
    expect(await screen.findByRole('button', { name: 'Going' })).toBeInTheDocument();
  });

  it('clears the mark when the toggle is clicked again', async () => {
    stubGoingList(['e1']);
    renderCard(vi.fn());

    fireEvent.click(await screen.findByRole('button', { name: 'Going' }));

    await waitFor(() => expect(resetEventGoing).toHaveBeenCalledWith('e1'));
    expect(await screen.findByRole('button', { name: "I'm going!" })).toBeInTheDocument();
    expect(markEventGoing).not.toHaveBeenCalled();
  });

  // Hiding a show you have said you are going to would strand it: the calendar
  // is where the going mark can be taken back.
  it('disables Hide while the user is going', async () => {
    stubGoingList(['e1']);
    renderCard(vi.fn());

    await screen.findByRole('button', { name: 'Going' });
    expect(screen.getByRole('button', { name: 'Hide' })).toBeDisabled();
  });

  it('leaves Hide enabled when the user is not going', async () => {
    stubGoingList();
    renderCard(vi.fn());

    await screen.findByRole('button', { name: "I'm going!" });
    expect(screen.getByRole('button', { name: 'Hide' })).toBeEnabled();
  });

  // The toggle sits inside the card, and the card navigates on click.
  it('does not open the event when the toggle is clicked', async () => {
    stubGoingList();
    renderRouted();

    fireEvent.click(await screen.findByRole('button', { name: "I'm going!" }));

    expect(screen.queryByText('Event detail page')).not.toBeInTheDocument();
  });
});
