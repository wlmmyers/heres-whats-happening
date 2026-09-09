import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react';
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

const older: CalendarEvent = {
  id: 'e00',
  title: 'Spring Show',
  starts_at: '2026-03-10T20:00:00Z',
  venue: { name: 'The Chapel' },
  score: 0,
  matched_because: { performers: [], genres: [] },
};

const wentToggle = () => screen.getByRole('button', { name: /^Went/ });

// The past rows live in the element the toggle controls, which is also where
// their order is asserted.
function wentRows() {
  const bodyId = wentToggle().getAttribute('aria-controls');
  expect(bodyId).toBeTruthy();
  const body = document.getElementById(bodyId!);
  expect(body).not.toBeNull();
  return within(body!).getAllByRole('listitem');
}

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

  // The endpoint returns past events too; they belong under Went, never in the
  // upcoming list or its count.
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

  it('collapses past shows behind a Went toggle that counts them', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([past, older, upcoming]);
    renderList();

    await screen.findByText('PB Live');
    expect(wentToggle()).toHaveTextContent('Went (2)');
    expect(wentToggle()).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByText('Last Month Show')).not.toBeInTheDocument();
  });

  it('reveals the past shows when Went is expanded', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([past, upcoming]);
    renderList();

    fireEvent.click(await screen.findByRole('button', { name: /^Went/ }));

    expect(await screen.findByText('Last Month Show')).toBeInTheDocument();
    expect(screen.getByText('The Basement')).toBeInTheDocument();
    expect(wentToggle()).toHaveAttribute('aria-expanded', 'true');
  });

  it('hides the past shows again when Went is collapsed', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([past, upcoming]);
    renderList();

    fireEvent.click(await screen.findByRole('button', { name: /^Went/ }));
    await screen.findByText('Last Month Show');
    fireEvent.click(wentToggle());

    await waitFor(() => expect(screen.queryByText('Last Month Show')).not.toBeInTheDocument());
  });

  // The going list runs forwards in time; history reads better backwards, so
  // the show you just came home from is at the top.
  it('lists the past shows most recent first', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([older, past, upcoming]);
    renderList();

    fireEvent.click(await screen.findByRole('button', { name: /^Went/ }));
    await screen.findByText('Spring Show');

    const rows = wentRows();
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveTextContent('Last Month Show');
    expect(rows[1]).toHaveTextContent('Spring Show');
  });

  it('offers no Went toggle when nothing has happened yet', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([upcoming]);
    renderList();

    await screen.findByText('PB Live');
    expect(screen.queryByRole('button', { name: /^Went/ })).not.toBeInTheDocument();
  });

  // The empty prompt covers the upcoming half only -- a user with history but
  // no plans still gets to see the history.
  it('shows past shows even when nothing is upcoming', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([past]);
    renderList();

    expect(await screen.findByText(/No upcoming shows yet/)).toBeInTheDocument();
    fireEvent.click(wentToggle());
    expect(await screen.findByText('Last Month Show')).toBeInTheDocument();
  });

  it('opens the event when a past row is clicked', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([past, upcoming]);
    renderList();

    fireEvent.click(await screen.findByRole('button', { name: /^Went/ }));
    fireEvent.click(await screen.findByText('Last Month Show'));

    expect(screen.getByText('Event detail page')).toBeInTheDocument();
  });

  // A show marked going by mistake can still be taken off the list after it
  // has passed.
  it('removes a past show when its Remove button is clicked', async () => {
    vi.mocked(listGoingEvents)
      .mockResolvedValueOnce([past, upcoming])
      .mockResolvedValue([upcoming]);
    vi.mocked(resetEventGoing).mockResolvedValue(undefined);
    renderList();

    fireEvent.click(await screen.findByRole('button', { name: /^Went/ }));
    fireEvent.click(await screen.findByRole('button', { name: /Remove Last Month Show/ }));

    await waitFor(() => expect(resetEventGoing).toHaveBeenCalledWith('e0'));
    await waitFor(() => expect(screen.queryByRole('button', { name: /^Went/ })).toBeNull());
  });
});
