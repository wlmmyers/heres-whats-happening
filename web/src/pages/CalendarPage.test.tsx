import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import CalendarPage from './CalendarPage';

vi.mock('../api/calendar', () => ({
  getCalendar: vi.fn(),
  getEvent: vi.fn(),
}));

import * as calApi from '../api/calendar';

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
});
