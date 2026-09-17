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

vi.mock('../api/manualGoingEvents', () => ({
  listManualGoingEvents: vi.fn(),
  createManualGoingEvent: vi.fn(),
  updateManualGoingEvent: vi.fn(),
  deleteManualGoingEvent: vi.fn(),
}));

vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));

import { listGoingEvents, resetEventGoing } from '../api/eventGoing';
import {
  listManualGoingEvents,
  createManualGoingEvent,
  deleteManualGoingEvent,
  type ManualGoingEvent,
} from '../api/manualGoingEvents';
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

const manualFuture: ManualGoingEvent = {
  id: 'm1',
  date: '2026-08-20',
  event_name: 'Manual Future Show',
  venue_name: 'Manual Venue',
  created_at: '2026-01-01T00:00:00Z',
};

const manualOld: ManualGoingEvent = {
  id: 'm0',
  date: '2019-08-03',
  event_name: 'Manual Old Show',
  venue_name: 'Old Venue',
  created_at: '2026-01-01T00:00:00Z',
};

const wentToggle = () => screen.getByRole('button', { name: /^Past Shows/ });

// Removal is confirmed through a ConfirmDialog, whose two buttons are named
// exactly — the row's own control is "Remove <title> from your going list", so
// an anchored match keeps the two apart.
const confirmRemoval = () => screen.getByRole('button', { name: 'Remove' });
const cancelRemoval = () => screen.getByRole('button', { name: 'Cancel' });

// The upcoming rows are the ones outside the collapsible past body.
function upcomingRows() {
  const bodyId = wentToggle().getAttribute('aria-controls');
  const body = bodyId ? document.getElementById(bodyId) : null;
  return screen.queryAllByRole('listitem').filter((li) => !body?.contains(li));
}

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
  // The Past Shows section remembers whether it was open in localStorage, and
  // that outlives a render. Without this, a test that expands the section
  // leaves the next one starting expanded — so its own click collapses rather
  // than reveals.
  localStorage.clear();
  vi.setSystemTime(new Date('2026-06-01T00:00:00Z'));
  // Most tests care only about the going list; default the hand-added one to
  // empty so each can opt in.
  vi.mocked(listManualGoingEvents).mockResolvedValue([]);
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

afterEach(() => {
  vi.useRealTimers();
});

describe('GoingWentList', () => {
  it('lists the shows the user is going to', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([upcoming]);
    renderList();

    expect(await screen.findByText('PB Live')).toBeInTheDocument();
    expect(screen.getByText('The Bowl')).toBeInTheDocument();
    expect(upcomingRows()).toHaveLength(1);
  });

  // The endpoint returns past events too; they belong under Went, never in the
  // upcoming list or its count.
  it('leaves out shows that have already happened', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([past, upcoming]);
    renderList();

    expect(await screen.findByText('PB Live')).toBeInTheDocument();
    expect(screen.queryByText('Last Month Show')).not.toBeInTheDocument();
    expect(upcomingRows()).toHaveLength(1);
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
    expect(upcomingRows()).toHaveLength(0);
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
    fireEvent.click(confirmRemoval());

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

  it('collapses past shows behind the Past Shows toggle', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([past, older, upcoming]);
    renderList();

    await screen.findByText('PB Live');
    expect(wentToggle()).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByText('Last Month Show')).not.toBeInTheDocument();
  });

  it('reveals the past shows when Went is expanded', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([past, upcoming]);
    renderList();

    fireEvent.click(await screen.findByRole('button', { name: /^Past Shows/ }));

    expect(await screen.findByText('Last Month Show')).toBeInTheDocument();
    expect(screen.getByText('The Basement')).toBeInTheDocument();
    expect(wentToggle()).toHaveAttribute('aria-expanded', 'true');
  });

  it('hides the past shows again when Went is collapsed', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([past, upcoming]);
    renderList();

    fireEvent.click(await screen.findByRole('button', { name: /^Past Shows/ }));
    await screen.findByText('Last Month Show');
    fireEvent.click(wentToggle());

    await waitFor(() => expect(screen.queryByText('Last Month Show')).not.toBeInTheDocument());
  });

  // The going list runs forwards in time; history reads better backwards, so
  // the show you just came home from is at the top.
  it('lists the past shows most recent first', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([older, past, upcoming]);
    renderList();

    fireEvent.click(await screen.findByRole('button', { name: /^Past Shows/ }));
    await screen.findByText('Spring Show');

    const rows = wentRows();
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveTextContent('Last Month Show');
    expect(rows[1]).toHaveTextContent('Spring Show');
  });

  // The heading stays put with no history behind it: it carries the "+" that
  // adds a show by hand, which is exactly what a user with no past shows yet
  // needs to reach.
  it('keeps the Past Shows heading when nothing has happened yet', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([upcoming]);
    renderList();

    await screen.findByText('PB Live');
    expect(screen.getByRole('button', { name: /^Past Shows/ })).toBeInTheDocument();
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

    fireEvent.click(await screen.findByRole('button', { name: /^Past Shows/ }));
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

    fireEvent.click(await screen.findByRole('button', { name: /^Past Shows/ }));
    fireEvent.click(await screen.findByRole('button', { name: /Remove Last Month Show/ }));
    fireEvent.click(confirmRemoval());

    await waitFor(() => expect(resetEventGoing).toHaveBeenCalledWith('e0'));
    await waitFor(() => expect(screen.queryByText('Last Month Show')).not.toBeInTheDocument());
  });
  // ---- hand-added shows ----------------------------------------------------
  //
  // These come from a separate table and endpoint and carry no event id, so the
  // list merges them in rather than the server doing it. The clock is pinned to
  // 2026-06-01T00:00:00Z, which is 5pm on May 31st in the test timezone.

  it('lists a hand-added future show among the upcoming ones', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([upcoming]);
    vi.mocked(listManualGoingEvents).mockResolvedValue([manualFuture]);
    renderList();

    expect(await screen.findByText('Manual Future Show')).toBeInTheDocument();
    expect(screen.getByText('Manual Venue')).toBeInTheDocument();
    expect(upcomingRows()).toHaveLength(2);
  });

  it('files a hand-added past show under Past Shows', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([upcoming]);
    vi.mocked(listManualGoingEvents).mockResolvedValue([manualOld]);
    renderList();

    await screen.findByText('PB Live');
    expect(screen.queryByText('Manual Old Show')).not.toBeInTheDocument();

    fireEvent.click(wentToggle());
    expect(await screen.findByText('Manual Old Show')).toBeInTheDocument();
    expect(upcomingRows()).toHaveLength(1);
  });

  // A date-only show has no end time of its own, so the day itself is the end.
  // Today's show is still ahead of you at 5pm.
  it('keeps a hand-added show dated today under upcoming', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([]);
    vi.mocked(listManualGoingEvents).mockResolvedValue([
      { ...manualFuture, id: 'm-today', date: '2026-05-31', event_name: 'Tonight' },
    ]);
    renderList();

    expect(await screen.findByText('Tonight')).toBeInTheDocument();
    expect(upcomingRows()).toHaveLength(1);
  });

  it('moves a hand-added show to Past Shows the day after it happened', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([]);
    vi.mocked(listManualGoingEvents).mockResolvedValue([
      { ...manualFuture, id: 'm-yest', date: '2026-05-30', event_name: 'Yesterday' },
    ]);
    renderList();

    await screen.findByText(/No upcoming shows yet/);
    fireEvent.click(wentToggle());
    expect(await screen.findByText('Yesterday')).toBeInTheDocument();
  });

  it('orders hand-added shows against the real ones by date', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([upcoming]);
    vi.mocked(listManualGoingEvents).mockResolvedValue([
      { ...manualFuture, id: 'm-early', date: '2026-06-05', event_name: 'Earlier Manual' },
      { ...manualFuture, id: 'm-late', date: '2026-08-20', event_name: 'Later Manual' },
    ]);
    renderList();

    await screen.findByText('Later Manual');
    const titles = screen.getAllByRole('listitem').map((row) => row.textContent);
    expect(titles[0]).toContain('Earlier Manual');
    expect(titles[1]).toContain('PB Live');
    expect(titles[2]).toContain('Later Manual');
  });

  it('orders hand-added past shows most recent first, alongside the real ones', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([past]);
    vi.mocked(listManualGoingEvents).mockResolvedValue([
      { ...manualOld, id: 'm-2019', date: '2019-08-03', event_name: 'Ancient Manual' },
      { ...manualOld, id: 'm-may', date: '2026-05-20', event_name: 'Recent Manual' },
    ]);
    renderList();

    fireEvent.click(await screen.findByRole('button', { name: /^Past Shows/ }));
    await screen.findByText('Recent Manual');

    const rows = wentRows();
    expect(rows[0]).toHaveTextContent('Recent Manual');
    expect(rows[1]).toHaveTextContent('Last Month Show');
    expect(rows[2]).toHaveTextContent('Ancient Manual');
  });

  // The user never entered a time, so the row must not invent one.
  it('renders a hand-added show as a day with no time', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([]);
    vi.mocked(listManualGoingEvents).mockResolvedValue([manualFuture]);
    renderList();

    const row = (await screen.findByText('Manual Future Show')).closest('li');
    expect(row).not.toBeNull();
    expect(row!.textContent).toContain('Aug 20');
    expect(row!.textContent).not.toMatch(/\d{1,2}:\d{2}/);
  });

  it('deletes a hand-added show when its remove button is clicked', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([]);
    vi.mocked(listManualGoingEvents).mockResolvedValueOnce([manualFuture]).mockResolvedValue([]);
    vi.mocked(deleteManualGoingEvent).mockResolvedValue(undefined);
    renderList();

    fireEvent.click(await screen.findByRole('button', { name: /Remove Manual Future Show/ }));
    fireEvent.click(confirmRemoval());

    await waitFor(() => expect(deleteManualGoingEvent).toHaveBeenCalledWith('m1'));
    await waitFor(() => expect(screen.queryByText('Manual Future Show')).not.toBeInTheDocument());
  });

  // A hand-added show has no event id, so there is no detail page to open.
  it('does not navigate when a hand-added row is clicked', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([]);
    vi.mocked(listManualGoingEvents).mockResolvedValue([manualFuture]);
    renderList();

    fireEvent.click(await screen.findByText('Manual Future Show'));

    expect(screen.queryByText('Event detail page')).not.toBeInTheDocument();
  });

  it('opens the add dialog from the + in the Past Shows heading', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([]);
    renderList();

    expect(screen.queryByRole('dialog')).toBeNull();
    fireEvent.click(await screen.findByRole('button', { name: /add a show by hand/i }));

    expect(await screen.findByRole('dialog')).toBeInTheDocument();
  });

  // The + sits inside the heading next to the toggle; it must not also fold the
  // section closed on its way through.
  it('does not toggle the past section when the + is clicked', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([past]);
    renderList();

    await screen.findByText(/No upcoming shows yet/);
    const before = wentToggle().getAttribute('aria-expanded');
    fireEvent.click(screen.getByRole('button', { name: /add a show by hand/i }));

    expect(wentToggle()).toHaveAttribute('aria-expanded', before!);
  });

  // Adding a show that has already happened files it into a section that is
  // collapsed by default, so without this the dialog closes and nothing
  // visibly happens.
  it('opens the past section after a past show is added', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([]);
    vi.mocked(createManualGoingEvent).mockResolvedValue({
      id: 'm-new',
      date: '2019-08-03',
      event_name: 'Added Old Show',
      venue_name: 'Old Venue',
      created_at: '2026-01-01T00:00:00Z',
    });
    renderList();

    await screen.findByText(/No upcoming shows yet/);
    expect(wentToggle()).toHaveAttribute('aria-expanded', 'false');

    fireEvent.click(screen.getByRole('button', { name: /add a show by hand/i }));
    fireEvent.change(await screen.findByLabelText(/^date$/i), {
      target: { value: '2019-08-03' },
    });
    fireEvent.change(screen.getByLabelText(/^event name$/i), {
      target: { value: 'Added Old Show' },
    });
    fireEvent.change(screen.getByLabelText(/^venue name$/i), { target: { value: 'Old Venue' } });
    fireEvent.click(screen.getByRole('button', { name: /^add event$/i }));

    await waitFor(() => expect(wentToggle()).toHaveAttribute('aria-expanded', 'true'));
  });

  // An upcoming show lands in a list that is already on screen, so there is
  // nothing to reveal.
  it('leaves the past section alone after an upcoming show is added', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([]);
    vi.mocked(createManualGoingEvent).mockResolvedValue({
      id: 'm-new',
      date: '2026-08-20',
      event_name: 'Added Future Show',
      venue_name: 'Manual Venue',
      created_at: '2026-01-01T00:00:00Z',
    });
    renderList();

    await screen.findByText(/No upcoming shows yet/);
    fireEvent.click(screen.getByRole('button', { name: /add a show by hand/i }));
    fireEvent.change(await screen.findByLabelText(/^date$/i), {
      target: { value: '2026-08-20' },
    });
    fireEvent.change(screen.getByLabelText(/^event name$/i), {
      target: { value: 'Added Future Show' },
    });
    fireEvent.change(screen.getByLabelText(/^venue name$/i), {
      target: { value: 'Manual Venue' },
    });
    fireEvent.click(screen.getByRole('button', { name: /^add event$/i }));

    await waitFor(() => expect(createManualGoingEvent).toHaveBeenCalled());
    expect(wentToggle()).toHaveAttribute('aria-expanded', 'false');
  });

  // ---- confirming a removal ------------------------------------------------
  //
  // Both kinds of row are destructive to remove, so neither goes through on the
  // "x" alone. The two are worded apart because the consequences differ: a
  // going event can be marked again from the calendar, a hand-added one is gone.

  it('asks before taking a show off the going list', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([upcoming]);
    renderList();

    fireEvent.click(await screen.findByRole('button', { name: /Remove PB Live/ }));

    expect(await screen.findByRole('dialog')).toHaveTextContent('PB Live');
    expect(resetEventGoing).not.toHaveBeenCalled();
    expect(screen.getByText('PB Live')).toBeInTheDocument();
  });

  it('keeps the show when the removal is cancelled', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([upcoming]);
    renderList();

    fireEvent.click(await screen.findByRole('button', { name: /Remove PB Live/ }));
    fireEvent.click(cancelRemoval());

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(resetEventGoing).not.toHaveBeenCalled();
    expect(screen.getByText('PB Live')).toBeInTheDocument();
  });

  it('asks before deleting a hand-added show', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([]);
    vi.mocked(listManualGoingEvents).mockResolvedValue([manualFuture]);
    renderList();

    fireEvent.click(await screen.findByRole('button', { name: /Remove Manual Future Show/ }));

    expect(await screen.findByRole('dialog')).toHaveTextContent('Manual Future Show');
    expect(deleteManualGoingEvent).not.toHaveBeenCalled();
    expect(screen.getByText('Manual Future Show')).toBeInTheDocument();
  });

  it('keeps the hand-added show when the removal is cancelled', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([]);
    vi.mocked(listManualGoingEvents).mockResolvedValue([manualFuture]);
    renderList();

    fireEvent.click(await screen.findByRole('button', { name: /Remove Manual Future Show/ }));
    fireEvent.click(cancelRemoval());

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(deleteManualGoingEvent).not.toHaveBeenCalled();
    expect(screen.getByText('Manual Future Show')).toBeInTheDocument();
  });

  // Deleting a hand-added row destroys the only copy of it; un-marking a going
  // event does not. The prompt has to say so.
  it('warns that deleting a hand-added show is permanent', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([]);
    vi.mocked(listManualGoingEvents).mockResolvedValue([manualFuture]);
    renderList();

    fireEvent.click(await screen.findByRole('button', { name: /Remove Manual Future Show/ }));

    expect(await screen.findByRole('dialog')).toHaveTextContent(/permanently/i);
  });

  it('does not warn of a permanent delete when un-marking a going event', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([upcoming]);
    renderList();

    fireEvent.click(await screen.findByRole('button', { name: /Remove PB Live/ }));

    expect(await screen.findByRole('dialog')).not.toHaveTextContent(/permanently/i);
  });

  // The prompt is raised from a row inside the list; dismissing it must not
  // leave the next removal pointing at the wrong show.
  it('asks about the right show when a second removal follows a cancelled one', async () => {
    vi.mocked(listGoingEvents).mockResolvedValue([upcoming]);
    vi.mocked(listManualGoingEvents).mockResolvedValue([manualFuture]);
    renderList();

    fireEvent.click(await screen.findByRole('button', { name: /Remove PB Live/ }));
    fireEvent.click(cancelRemoval());
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());

    fireEvent.click(screen.getByRole('button', { name: /Remove Manual Future Show/ }));

    const dialog = await screen.findByRole('dialog');
    expect(dialog).toHaveTextContent('Manual Future Show');
    expect(dialog).not.toHaveTextContent('PB Live');
  });
});
