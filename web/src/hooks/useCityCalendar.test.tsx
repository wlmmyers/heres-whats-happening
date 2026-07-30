import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';

vi.mock('../api/calendar', () => ({
  getCityCalendar: vi.fn(),
}));

import { useCityCalendar } from './useCityCalendar';
import { getCityCalendar } from '../api/calendar';

function wrapper({ children }: { children: ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

beforeEach(() => {
  vi.resetAllMocks();
});

const cityEvent = {
  id: 'c1',
  title: 'Citywide Show',
  starts_at: '2026-06-15T20:00:00Z',
  venue: { name: 'Civic Hall' },
  score: 0,
  matched_because: { performers: [], genres: [] },
};

describe('useCityCalendar', () => {
  it('fetches the city calendar when enabled', async () => {
    vi.mocked(getCityCalendar).mockResolvedValueOnce([cityEvent]);
    const { result } = renderHook(
      () => useCityCalendar('city-1', '2026-01-01', '2026-04-01', true),
      {
        wrapper,
      },
    );
    await waitFor(() => expect(result.current.data).toEqual([cityEvent]));
    expect(getCityCalendar).toHaveBeenCalledWith('city-1', '2026-01-01', '2026-04-01');
  });

  it('does not fetch when disabled', async () => {
    renderHook(() => useCityCalendar('city-1', '2026-01-01', '2026-04-01', false), { wrapper });
    await new Promise((r) => setTimeout(r, 20));
    expect(getCityCalendar).not.toHaveBeenCalled();
  });

  it('does not fetch when the city is unknown', async () => {
    renderHook(() => useCityCalendar(undefined, '2026-01-01', '2026-04-01', true), { wrapper });
    await new Promise((r) => setTimeout(r, 20));
    expect(getCityCalendar).not.toHaveBeenCalled();
  });
});
