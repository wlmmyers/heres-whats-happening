import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';

vi.mock('../api/calendar', () => ({
  getCityCalendar: vi.fn(),
}));

vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));

import { useCityCalendar } from './useCityCalendar';
import { getCityCalendar } from '../api/calendar';
import { useAuth } from '../auth/useAuth';

let queryClient: QueryClient;

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

beforeEach(() => {
  vi.resetAllMocks();
  queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  vi.mocked(useAuth).mockReturnValue({
    status: 'authenticated',
    user: { id: 'u1', email: 'u@example.com', city_id: 'city-1', confirmed: true },
    login: vi.fn(),
    signup: vi.fn(),
    logout: vi.fn(),
    refreshUser: vi.fn(),
  });
});

const cityEvent = {
  id: 'c1',
  title: 'Citywide Show',
  starts_at: '2026-06-15T20:00:00Z',
  venue: { name: 'Civic Hall' },
  score: 0,
  matched_because: { performers: [], genres: [] },
};

const laterEvent = { ...cityEvent, id: 'c2', title: 'Later Show' };

describe('useCityCalendar', () => {
  it('fetches the first page with no cursor when the city is known', async () => {
    vi.mocked(getCityCalendar).mockResolvedValueOnce({ events: [cityEvent] });

    const { result } = renderHook(() => useCityCalendar('city-1'), { wrapper });

    await waitFor(() => expect(result.current.data?.pages[0].events).toEqual([cityEvent]));
    expect(getCityCalendar).toHaveBeenCalledWith('city-1', undefined);
  });

  it('does not fetch when the city is unknown', async () => {
    renderHook(() => useCityCalendar(undefined), { wrapper });
    await new Promise((r) => setTimeout(r, 20));
    expect(getCityCalendar).not.toHaveBeenCalled();
  });

  it('follows next_cursor when fetching the following page', async () => {
    vi.mocked(getCityCalendar)
      .mockResolvedValueOnce({ events: [cityEvent], next_cursor: 'cur-2' })
      .mockResolvedValueOnce({ events: [laterEvent] });

    const { result } = renderHook(() => useCityCalendar('city-1'), { wrapper });

    await waitFor(() => expect(result.current.hasNextPage).toBe(true));
    result.current.fetchNextPage();

    await waitFor(() => expect(result.current.data?.pages).toHaveLength(2));
    expect(getCityCalendar).toHaveBeenLastCalledWith('city-1', 'cur-2');
    expect(result.current.data?.pages.flatMap((p) => p.events)).toEqual([cityEvent, laterEvent]);
  });

  it('stops paging when the last page omits next_cursor', async () => {
    vi.mocked(getCityCalendar).mockResolvedValueOnce({ events: [cityEvent] });

    const { result } = renderHook(() => useCityCalendar('city-1'), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.hasNextPage).toBe(false);
  });

  // The page's event detail route reads ['event', userId, eventId]; seeding it
  // here is what makes opening a card from the list render without a refetch.
  it('seeds the event cache for each fetched event', async () => {
    vi.mocked(getCityCalendar).mockResolvedValueOnce({ events: [cityEvent] });

    const { result } = renderHook(() => useCityCalendar('city-1'), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(queryClient.getQueryData(['event', 'u1', 'c1'])).toEqual(cityEvent);
  });
});
