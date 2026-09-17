import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, act, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../api/search', () => ({ searchEvents: vi.fn() }));

const navigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom');
  return { ...actual, useNavigate: () => navigate };
});

import { searchEvents } from '../api/search';
import SearchDialog from './SearchDialog';

function renderDialog(onClose = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const utils = render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <SearchDialog open onClose={onClose} cityId="city-1" />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { ...utils, onClose };
}

const field = () => screen.getByRole('combobox');

const oneResult = {
  results: [
    {
      id: 'e1',
      title: 'Midnight Orchard',
      starts_at: '2026-10-02T03:00:00Z',
      venue: { name: 'The Bowl' },
    },
  ],
};

beforeEach(() => {
  vi.resetAllMocks();
  navigate.mockReset();
});

describe('SearchDialog', () => {
  it('renders nothing when closed', () => {
    const qc = new QueryClient();
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <SearchDialog open={false} onClose={vi.fn()} cityId="city-1" />
        </MemoryRouter>
      </QueryClientProvider>,
    );
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('prompts rather than querying below the minimum length', async () => {
    vi.useFakeTimers();
    try {
      vi.mocked(searchEvents).mockResolvedValue(oneResult);
      renderDialog();

      // fireEvent.change sets the value in one synchronous DOM event, rather
      // than userEvent's per-keystroke simulation (whose internal timers
      // don't mix cleanly with vi.useFakeTimers here). useDebouncedValue's
      // delay is 400ms; asserting immediately -- as the original version of
      // this test did -- would check the pre-debounce "" rather than "mi".
      // Both are under the floor, so that assertion couldn't tell "mi was
      // correctly computed as too short" apart from "debounced never left its
      // initial empty value". Advancing past the real delay first makes this
      // the settled "mi" state.
      fireEvent.change(field(), { target: { value: 'mi' } });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(400);
      });
      expect(searchEvents).not.toHaveBeenCalled();
      expect(screen.getByText(/type at least/i)).toBeInTheDocument();

      // Still not proof on its own: "" and "mi" render identically, so a
      // frozen, disconnected debounced value would pass the assertions above
      // too. Crossing the floor and confirming a query actually fires is what
      // proves debounced tracks the real input rather than coincidentally
      // agreeing with it while stuck.
      fireEvent.change(field(), { target: { value: 'midnight' } });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(400);
      });
      expect(searchEvents).toHaveBeenCalledWith('city-1', 'midnight');
    } finally {
      vi.useRealTimers();
    }
  });

  it('renders results as options', async () => {
    vi.mocked(searchEvents).mockResolvedValue(oneResult);
    const user = userEvent.setup();
    renderDialog();
    await user.type(field(), 'midnight');
    await waitFor(() => expect(screen.getByRole('option')).toHaveTextContent('Midnight Orchard'));
    expect(screen.getByRole('option')).toHaveTextContent('The Bowl');
  });

  it('shows an empty state when nothing matches', async () => {
    vi.mocked(searchEvents).mockResolvedValue({ results: [] });
    const user = userEvent.setup();
    renderDialog();
    await user.type(field(), 'zzzqqq');
    await waitFor(() => expect(screen.getByText(/no events match/i)).toBeInTheDocument());
  });

  it('navigates to the event on Enter and closes', async () => {
    vi.mocked(searchEvents).mockResolvedValue(oneResult);
    const user = userEvent.setup();
    const { onClose } = renderDialog();
    await user.type(field(), 'midnight');
    await waitFor(() => expect(screen.getByRole('option')).toBeInTheDocument());
    await user.keyboard('{ArrowDown}{Enter}');
    expect(navigate).toHaveBeenCalledWith('/events/e1');
    expect(onClose).toHaveBeenCalled();
  });

  it('closes on Escape', async () => {
    const user = userEvent.setup();
    const { onClose } = renderDialog();
    await user.keyboard('{Escape}');
    expect(onClose).toHaveBeenCalled();
  });

  it('closes from the close button', async () => {
    const user = userEvent.setup();
    const { onClose } = renderDialog();
    await user.click(screen.getByRole('button', { name: 'Close' }));
    // Once, not twice: the click must not also bubble to the backdrop.
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  // The failure this component is most likely to see, and the only place it is
  // visible at all: the endpoint has its own 60/min limiter, also spends the
  // shared 120/min authed budget, `retry: false` is set app-wide, and there is
  // deliberately no CloudWatch alarm on it. Falling through to the empty
  // branch tells the user -- definitively, and wrongly -- that nothing matches.
  it('reports a failed search instead of claiming nothing matched', async () => {
    vi.mocked(searchEvents).mockRejectedValue(new Error('429 Too Many Requests'));
    const user = userEvent.setup();
    renderDialog();
    await user.type(field(), 'midnight');

    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(/unavailable/i));
    expect(screen.queryByText(/no events match/i)).not.toBeInTheDocument();
  });

  // keepPreviousData keeps the last successful page in `data`, so an errored
  // refetch must not quietly go on presenting stale hits as current results.
  it('shows the failure rather than the previous results when a later search fails', async () => {
    vi.mocked(searchEvents).mockResolvedValueOnce(oneResult);
    const user = userEvent.setup();
    renderDialog();
    await user.type(field(), 'midnight');
    await waitFor(() => expect(screen.getByRole('option')).toBeInTheDocument());

    vi.mocked(searchEvents).mockRejectedValue(new Error('500'));
    await user.type(field(), ' orchard');

    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(/unavailable/i));
    expect(screen.queryByRole('option')).not.toBeInTheDocument();
  });

  // The server truncates at 100 runes, so anything past that is typed, sent,
  // and silently discarded. Matching the ceiling on the client makes the rule
  // the same at both ends rather than only at the floor.
  it('caps the input at the length the server truncates to', () => {
    renderDialog();
    expect(field()).toHaveAttribute('maxLength', '100');
  });

  // starts_at is fetched, serialized and typed, but was never displayed, so a
  // multi-night run rendered as N identical rows.
  //
  // 'Oct 1' is also the assertion that the value went through a real date
  // formatter: the ISO string says 2026-10-02, and only converting it to the
  // suite's pinned America/Los_Angeles zone turns that into October 1st. A row
  // that sliced the ISO string would have to say 02.
  it('renders the date so a multi-night run is distinguishable', async () => {
    vi.mocked(searchEvents).mockResolvedValue(oneResult);
    const user = userEvent.setup();
    renderDialog();
    await user.type(field(), 'midnight');

    await waitFor(() => expect(screen.getByRole('option')).toBeInTheDocument());
    expect(screen.getByRole('option')).toHaveTextContent(/Oct 1/);
    expect(screen.getByRole('option')).not.toHaveTextContent('2026-10-02T03:00:00Z');
  });

  it('does not claim the listbox is expanded once a query shrinks back below the minimum', async () => {
    vi.mocked(searchEvents).mockResolvedValue(oneResult);
    const user = userEvent.setup();
    renderDialog();
    await user.type(field(), 'midnight');
    await waitFor(() => expect(screen.getByRole('option')).toBeInTheDocument());

    // keepPreviousData means the earlier hit can still be sitting in `data`
    // even once the query is back under SEARCH_MIN_LENGTH and disabled --
    // that must not leave the combobox's ARIA wiring pointing at a listbox
    // that the component has actually stopped rendering.
    await user.clear(field());
    await user.type(field(), 'mi');
    await waitFor(() => expect(screen.getByText(/type at least/i)).toBeInTheDocument());

    expect(field()).toHaveAttribute('aria-expanded', 'false');
    expect(field()).not.toHaveAttribute('aria-controls');
    expect(field()).not.toHaveAttribute('aria-activedescendant');
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
  });
});
