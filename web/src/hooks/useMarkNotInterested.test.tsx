import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider, type InfiniteData } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import type { CalendarResponse } from '../api/calendar';

vi.mock('../api/notInterested', () => ({
  markNotInterested: vi.fn(),
}));

vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));

import { useMarkNotInterested } from './useMarkNotInterested';
import { markNotInterested } from '../api/notInterested';
import { useAuth } from '../auth/useAuth';
import { calendarQueryKey } from './useCalendar';

let queryClient: QueryClient;

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

function event(id: string) {
  return {
    id,
    title: `Event ${id}`,
    starts_at: '2026-06-15T20:00:00Z',
    venue: { name: 'The Bowl' },
    score: 0.5,
    matched_because: { performers: [], genres: [] },
  };
}

// The calendar is an infinite query, so its cache entry is InfiniteData —
// pages of CalendarResponse, not a flat event array.
function seedCalendar(pages: CalendarResponse[]) {
  queryClient.setQueryData<InfiniteData<CalendarResponse>>(calendarQueryKey('u1'), {
    pages,
    pageParams: pages.map((_, i) => (i === 0 ? undefined : `cur-${i}`)),
  });
}

function cachedEventIds() {
  return queryClient
    .getQueryData<InfiniteData<CalendarResponse>>(calendarQueryKey('u1'))
    ?.pages.flatMap((p) => p.events)
    .map((e) => e.id);
}

beforeEach(() => {
  vi.resetAllMocks();
  queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
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

describe('useMarkNotInterested', () => {
  it('calls the API with the dismissed event id', async () => {
    seedCalendar([{ events: [event('e1'), event('e2')] }]);
    vi.mocked(markNotInterested).mockResolvedValue(undefined);

    const { result } = renderHook(() => useMarkNotInterested(), { wrapper });
    result.current.mutate('e1');

    await waitFor(() => expect(markNotInterested).toHaveBeenCalledWith('e1'));
  });

  it('optimistically removes the event from every cached page', async () => {
    seedCalendar([
      { events: [event('e1'), event('e2')], next_cursor: 'cur-1' },
      { events: [event('e3')] },
    ]);
    vi.mocked(markNotInterested).mockResolvedValue(undefined);

    const { result } = renderHook(() => useMarkNotInterested(), { wrapper });
    result.current.mutate('e2');

    await waitFor(() => expect(cachedEventIds()).toEqual(['e1', 'e3']));
  });

  it('restores the previous pages when the API call fails', async () => {
    seedCalendar([{ events: [event('e1'), event('e2')] }]);
    vi.mocked(markNotInterested).mockRejectedValue(new Error('boom'));

    const { result } = renderHook(() => useMarkNotInterested(), { wrapper });
    result.current.mutate('e1');

    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(cachedEventIds()).toEqual(['e1', 'e2']);
  });
});
