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
      expect(screen.getByText(/keep typing/i)).toBeInTheDocument();

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
    await waitFor(() => expect(screen.getByText(/keep typing/i)).toBeInTheDocument());

    expect(field()).toHaveAttribute('aria-expanded', 'false');
    expect(field()).not.toHaveAttribute('aria-controls');
    expect(field()).not.toHaveAttribute('aria-activedescendant');
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
  });
});
