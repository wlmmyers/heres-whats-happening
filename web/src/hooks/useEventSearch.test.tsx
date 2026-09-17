import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';

vi.mock('../api/search', () => ({ searchEvents: vi.fn() }));

import { searchEvents, type SearchResponse } from '../api/search';
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

  // '😀😀' is 2 runes but 4 UTF-16 code units (each emoji is a surrogate
  // pair). A `.length`-based floor check would see 4 >= 3 and fire; the
  // correct rune-counted check sees 2 < 3 and stays idle. A same-length
  // BMP string (e.g. '東京都', 3 runes and 3 code units) can't tell the two
  // implementations apart, which is why that string was replaced here.
  it('counts runes, not UTF-16 code units', () => {
    renderHook(() => useEventSearch('city-1', '😀😀'), { wrapper });
    expect(searchEvents).not.toHaveBeenCalled();
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

  // Guards the config, not just its presence: without `placeholderData:
  // keepPreviousData` this would drop to `data: undefined` the instant the
  // query key changes, which is the dropdown-blanking/strobing regression
  // the hook exists to prevent.
  it('keeps the previous results visible while a new query is in flight', async () => {
    const first: SearchResponse = {
      results: [
        {
          id: 'e1',
          title: 'Midnight Orchard',
          starts_at: '2026-10-02T03:00:00Z',
          venue: { name: 'The Bowl' },
        },
      ],
    };
    const second: SearchResponse = {
      results: [
        {
          id: 'e2',
          title: 'Midnight Rider',
          starts_at: '2026-10-03T03:00:00Z',
          venue: { name: 'The Barn' },
        },
      ],
    };

    // A promise we resolve by hand so the second request can be observed
    // mid-flight instead of resolving before assertions run.
    let resolveSecond!: (value: SearchResponse) => void;
    const secondRequest = new Promise<SearchResponse>((resolve) => {
      resolveSecond = resolve;
    });
    vi.mocked(searchEvents).mockResolvedValueOnce(first).mockReturnValueOnce(secondRequest);

    const { result, rerender } = renderHook(({ q }) => useEventSearch('city-1', q), {
      wrapper,
      initialProps: { q: 'midnight' },
    });
    await waitFor(() => expect(result.current.data).toEqual(first));

    // A new query string is a new queryKey, so this starts a fresh request
    // rather than reusing the cached one.
    rerender({ q: 'midnights' });

    await waitFor(() => expect(result.current.isFetching).toBe(true));
    expect(result.current.data).toEqual(first);
    expect(result.current.isPlaceholderData).toBe(true);

    resolveSecond(second);
    await waitFor(() => expect(result.current.data).toEqual(second));
    expect(result.current.isPlaceholderData).toBe(false);
  });
});
