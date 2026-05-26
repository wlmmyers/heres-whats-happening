import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import EventDetailPage from './EventDetailPage';

vi.mock('../api/calendar', () => ({
  getCalendar: vi.fn(),
  getEvent: vi.fn(),
}));

import * as calApi from '../api/calendar';

function renderAt(path: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/events/:id" element={<EventDetailPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
});

describe('EventDetailPage', () => {
  it('renders the event detail', async () => {
    (calApi.getEvent as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      id: 'e1',
      title: 'PB Live',
      description: 'indie rock concert',
      starts_at: '2026-06-15T20:00:00Z',
      venue: { name: 'The Bowl', address: '100 Main St' },
      url: 'https://tix.example/aaa',
      score: 0.82,
      matched_because: { performers: ['Phoebe Bridgers'], genres: ['indie'] },
    });
    renderAt('/events/e1');
    await waitFor(() => expect(screen.getByText('PB Live')).toBeInTheDocument());
    expect(screen.getByText(/The Bowl/)).toBeInTheDocument();
    expect(screen.getByText(/100 Main St/)).toBeInTheDocument();
    expect(screen.getByText(/82% match/)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /tickets|view event/i })).toHaveAttribute('href', 'https://tix.example/aaa');
  });

  it('renders 404 if event not found', async () => {
    const err = Object.assign(new Error('not found'), { status: 404, code: 'not_found' });
    (calApi.getEvent as ReturnType<typeof vi.fn>).mockRejectedValueOnce(err);
    renderAt('/events/missing');
    await waitFor(() => expect(screen.getByText(/event not found/i)).toBeInTheDocument());
  });
});
