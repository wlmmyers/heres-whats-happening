import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';

vi.mock('../api/search', () => ({ searchEvents: vi.fn() }));

import { searchEvents } from '../api/search';
import { useEventSearch } from './useEventSearch';

let queryClient: QueryClient;

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

beforeEach(() => {
  vi.resetAllMocks();
  queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
});

describe('useEventSearch', () => {
  it('stays idle while the city is unknown', () => {
    renderHook(() => useEventSearch(undefined, 'midnight'), { wrapper });
    expect(searchEvents).not.toHaveBeenCalled();
  });

  // Mirrors the server's 3-rune floor so a short query never round-trips for
  // a 400.
  it('stays idle below three characters', () => {
    renderHook(() => useEventSearch('city-1', 'mi'), { wrapper });
    expect(searchEvents).not.toHaveBeenCalled();
  });

  it('counts runes, not UTF-16 code units', () => {
    vi.mocked(searchEvents).mockResolvedValue({ results: [] });
    renderHook(() => useEventSearch('city-1', '東京都'), { wrapper });
    expect(searchEvents).toHaveBeenCalledWith('city-1', '東京都');
  });

  it('queries once the floor is met', async () => {
    vi.mocked(searchEvents).mockResolvedValue({
      results: [
        {
          id: 'e1',
          title: 'Midnight Orchard',
          starts_at: '2026-10-02T03:00:00Z',
          venue: { name: 'The Bowl' },
        },
      ],
    });
    const { result } = renderHook(() => useEventSearch('city-1', 'midnight'), { wrapper });
    await waitFor(() => expect(result.current.data?.results).toHaveLength(1));
    expect(searchEvents).toHaveBeenCalledWith('city-1', 'midnight');
  });
});
