import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import EventDetailPage from './EventDetailPage';
import * as s from './EventDetailPage.css';

vi.mock('../api/calendar', () => ({
  getCalendar: vi.fn(),
  getEvent: vi.fn(),
}));
vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));

import * as calApi from '../api/calendar';
import { useAuth } from '../auth/useAuth';

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
  vi.mocked(useAuth).mockReturnValue({
    status: 'authenticated',
    user: { id: 'u1', email: 'a@x', city_id: 'city-1', confirmed: true },
    login: vi.fn(),
    signup: vi.fn(),
    logout: vi.fn(),
    refreshUser: vi.fn(),
  });
});

// ICU pads its range patterns with thin and narrow no-break spaces; compare
// against plain spaces so the expectations stay readable.
function dateLine(container: HTMLElement) {
  return container.querySelector(`.${s.date}`)?.textContent?.replace(/[\u2009\u202f\u00a0]/g, ' ');
}

describe('EventDetailPage', () => {
  it('shows only the start time when the event has no ends_at', async () => {
    (calApi.getEvent as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      id: 'e1',
      title: 'PB Live',
      starts_at: '2026-06-15T20:00:00Z',
      venue: { name: 'The Bowl' },
      score: 0,
      matched_because: { performers: [], genres: [] },
    });
    const { container } = renderAt('/events/e1');
    await waitFor(() => expect(screen.getByText('PB Live')).toBeInTheDocument());
    expect(dateLine(container)).toBe('Monday, June 15, 2026 at 1:00 PM');
  });

  it('shows the start–end range when the event has an ends_at', async () => {
    (calApi.getEvent as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      id: 'e1',
      title: 'PB Live',
      starts_at: '2026-06-15T20:00:00Z',
      ends_at: '2026-06-15T23:30:00Z',
      venue: { name: 'The Bowl' },
      score: 0,
      matched_because: { performers: [], genres: [] },
    });
    const { container } = renderAt('/events/e1');
    await waitFor(() => expect(screen.getByText('PB Live')).toBeInTheDocument());
    expect(dateLine(container)).toBe('Monday, June 15, 2026, 1:00 – 4:30 PM');
  });

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
    expect(screen.getByRole('link', { name: /tickets|view event/i })).toHaveAttribute(
      'href',
      'https://tix.example/aaa',
    );
  });

  it('renders the image tile inside the header when the event has an image_url', async () => {
    (calApi.getEvent as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      id: 'e1',
      title: 'PB Live',
      starts_at: '2026-06-15T20:00:00Z',
      image_url: 'https://cdn.test/pb.jpg',
      venue: { name: 'The Bowl' },
      score: 0.82,
      matched_because: { performers: [], genres: [] },
    });
    const { container } = renderAt('/events/e1');
    await waitFor(() => expect(screen.getByText('PB Live')).toBeInTheDocument());
    const img = container.querySelector(`.${s.detail} img`);
    expect(img).toHaveAttribute('src', 'https://cdn.test/pb.jpg');
  });

  it('renders 404 if event not found', async () => {
    const err = Object.assign(new Error('not found'), {
      status: 404,
      code: 'not_found',
    });
    (calApi.getEvent as ReturnType<typeof vi.fn>).mockRejectedValueOnce(err);
    renderAt('/events/missing');
    await waitFor(() => expect(screen.getByText(/event not found/i)).toBeInTheDocument());
  });

  it('shows the match score for a matched event', async () => {
    (calApi.getEvent as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      id: 'e1',
      title: 'PB Live',
      starts_at: '2026-06-15T20:00:00Z',
      venue: { name: 'The Bowl' },
      score: 0.82,
      matched_because: { performers: ['Phoebe Bridgers'], genres: ['indie'] },
    });
    renderAt('/events/e1');
    await waitFor(() => expect(screen.getByText('PB Live')).toBeInTheDocument());
    expect(screen.getByText(/82% match/)).toBeInTheDocument();
  });

  it('hides the match score for an unmatched city event', async () => {
    (calApi.getEvent as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      id: 'c1',
      title: 'Citywide Show',
      starts_at: '2026-06-15T20:00:00Z',
      venue: { name: 'Civic Hall' },
      score: 0,
      matched_because: { performers: [], genres: [] },
    });
    renderAt('/events/c1');
    await waitFor(() => expect(screen.getByText('Citywide Show')).toBeInTheDocument());
    expect(screen.queryByText(/% match/)).not.toBeInTheDocument();
  });
});
