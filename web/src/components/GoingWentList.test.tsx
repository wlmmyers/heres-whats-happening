import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import GoingWentList from './GoingWentList';
import type { CalendarEvent } from '../api/calendar';

vi.mock('../api/eventGoing', () => ({
  listGoingEvents: vi.fn(),
  listGoing: vi.fn(),
  markEventGoing: vi.fn(),
  resetEventGoing: vi.fn(),
}));

vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));

import { listGoingEvents, resetEventGoing } from '../api/eventGoing';
import { useAuth } from '../auth/useAuth';

const upcoming: CalendarEvent = {
  id: 'e1',
  title: 'PB Live',
  starts_at: '2026-06-15T20:00:00Z',
  venue: { name: 'The Bowl' },
  score: 0.82,
  matched_because: { performers: ['Phoebe Bridgers'], genres: ['indie'] },
};

const past: CalendarEvent = {
  id: 'e0',
  title: 'Last Month Show',
  starts_at: '2026-05-01T20:00:00Z',
  venue: { name: 'The Basement' },
  score: 0,
  matched_because: { performers: [], genres: [] },
};

function renderList() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/']}>
        <Routes>
          <Route path="/" element={<GoingWentList />} />
          <Route path="/events/:id" element={<div>Event detail page</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  vi.setSystemTime(new Date('2026-06-01T00:00:00Z'));
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

describe('GoingWentList', () => {
  it('lists the shows the user is going to', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([upcoming]);
    renderList();

    expect(await screen.findByText('PB Live')).toBeInTheDocument();
    expect(screen.getByText('The Bowl')).toBeInTheDocument();
    expect(screen.getByText('1 upcoming show')).toBeInTheDocument();
  });

  // The endpoint deliberately returns past events too, for the "went" half this
  // panel does not render yet -- they must not leak into the going list.
  it('leaves out shows that have already happened', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([past, upcoming]);
    renderList();

    expect(await screen.findByText('PB Live')).toBeInTheDocument();
    expect(screen.queryByText('Last Month Show')).not.toBeInTheDocument();
    expect(screen.getByText('1 upcoming show')).toBeInTheDocument();
  });

  // An event that started but has not ended is still one the user is at.
  it('keeps a show that is under way', async () => {
    vi.setSystemTime(new Date('2026-06-15T21:00:00Z'));
    vi.mocked(listGoingEvents).mockResolvedValue([
      { ...upcoming, starts_at: '2026-06-15T20:00:00Z', ends_at: '2026-06-15T23:30:00Z' },
    ]);
    renderList();

    expect(await screen.findByText('PB Live')).toBeInTheDocument();
  });

  it('prompts when nothing is marked going', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([]);
    renderList();

    expect(await screen.findByText(/No upcoming shows yet/)).toBeInTheDocument();
    // The count badge in the heading is hidden when the list is empty.
    expect(screen.queryByText(/^\d+ upcoming shows?$/)).not.toBeInTheDocument();
  });

  it('reports a failed load rather than an empty list', async () => {
    vi.mocked(listGoingEvents).mockRejectedValue(new Error('boom'));
    renderList();

    expect(await screen.findByText(/Couldn't load your going list/)).toBeInTheDocument();
  });

  it('opens the event when a row is clicked', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([upcoming]);
    renderList();

    fireEvent.click(await screen.findByText('PB Live'));
    expect(screen.getByText('Event detail page')).toBeInTheDocument();
  });

  it('drops the show from the list when Remove is clicked', async () => {
    // First load has the show; the refetch the mutation triggers no longer
    // does, as the server would report it after the delete.
    vi.mocked(listGoingEvents).mockResolvedValueOnce([upcoming]).mockResolvedValue([]);
    vi.mocked(resetEventGoing).mockResolvedValue(undefined);
    renderList();

    fireEvent.click(await screen.findByRole('button', { name: /Remove PB Live/ }));

    // The mutation's onMutate awaits before the request goes out, so the call
    // lands a tick after the click.
    await waitFor(() => expect(resetEventGoing).toHaveBeenCalledWith('e1'));
    await waitFor(() => expect(screen.queryByText('PB Live')).not.toBeInTheDocument());
  });

  // Remove sits inside the row, and the row navigates on click.
  it('does not open the event when Remove is clicked', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([upcoming]);
    vi.mocked(resetEventGoing).mockResolvedValue(undefined);
    renderList();

    fireEvent.click(await screen.findByRole('button', { name: /Remove PB Live/ }));

    expect(screen.queryByText('Event detail page')).not.toBeInTheDocument();
  });
});
