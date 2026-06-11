import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import CalendarPage from './CalendarPage';

vi.mock('../api/calendar', () => ({
  getCalendar: vi.fn(),
  getEvent: vi.fn(),
}));

vi.mock('../api/notInterested', () => ({
  markNotInterested: vi.fn(),
  resetNotInterested: vi.fn(),
}));

import * as calApi from '../api/calendar';
import * as niApi from '../api/notInterested';

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/calendar']}>
        <Routes>
          <Route path="/calendar" element={<CalendarPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
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

  it('shows empty state when there are no matches', async () => {
    (calApi.getCalendar as ReturnType<typeof vi.fn>).mockResolvedValueOnce([]);
    renderPage();
    await waitFor(() => expect(screen.getByText(/no upcoming matches yet/i)).toBeInTheDocument());
  });

  it('defaults to a 3-month range and changes the range when toggled', async () => {
    const getCal = calApi.getCalendar as ReturnType<typeof vi.fn>;
    getCal.mockResolvedValue([]);
    renderPage();

    await waitFor(() => expect(getCal).toHaveBeenCalled());
    expect(screen.getByText('Show events for next:')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '3 months' })).toHaveAttribute('aria-pressed', 'true');

    const threeMonthTo = getCal.mock.calls[0][1] as string;

    fireEvent.click(screen.getByRole('button', { name: '6 months' }));

    await waitFor(() =>
      expect(screen.getByRole('button', { name: '6 months' })).toHaveAttribute('aria-pressed', 'true'),
    );
    const lastCall = getCal.mock.calls[getCal.mock.calls.length - 1];
    expect(lastCall[1] > threeMonthTo).toBe(true);

    fireEvent.click(screen.getByRole('button', { name: '1 month' }));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: '1 month' })).toHaveAttribute('aria-pressed', 'true'),
    );
    const oneMonthTo = getCal.mock.calls[getCal.mock.calls.length - 1][1] as string;
    expect(oneMonthTo < threeMonthTo).toBe(true);
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
