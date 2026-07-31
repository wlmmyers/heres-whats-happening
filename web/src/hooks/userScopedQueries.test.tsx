import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';

vi.mock('../api/calendar', () => ({ getCalendar: vi.fn(), getEvent: vi.fn() }));
vi.mock('../api/spotify', () => ({ getSpotifyStatus: vi.fn() }));
vi.mock('../api/spotifyInterests', () => ({ listSpotifyInterests: vi.fn() }));
vi.mock('../api/manualInterests', () => ({ listManualInterests: vi.fn() }));
vi.mock('../api/auth', () => ({ getMe: vi.fn() }));
vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));

import { useAuth } from '../auth/useAuth';
import { getCalendar, getEvent } from '../api/calendar';
import { getSpotifyStatus } from '../api/spotify';
import { listSpotifyInterests } from '../api/spotifyInterests';
import { listManualInterests } from '../api/manualInterests';
import { getMe } from '../api/auth';
import { useCalendar } from './useCalendar';
import { useSpotifyStatus } from './useSpotifyStatus';
import { useManualInterests } from './useManualInterests';
import { useSpotifyInterests } from './useSpotifyInterests';
import { useMe } from './useMe';
import { useEvent } from './useEvent';

let queryClient: QueryClient;

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

const user = { id: 'u1', email: 'u@example.com', city_id: 'city-1', confirmed: true };
const actions = { login: vi.fn(), signup: vi.fn(), logout: vi.fn(), refreshUser: vi.fn() };
const authLoading = { ...actions, status: 'loading' as const, user: null };
const authResolved = { ...actions, status: 'authenticated' as const, user };

beforeEach(() => {
  vi.resetAllMocks();
  queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
});

const settle = () => new Promise((r) => setTimeout(r, 20));

// Every one of these keys its cache entry on user.id, so a query that runs
// before AuthProvider resolves lands under `undefined` and has to fetch a
// second time under the real id once the user arrives.
// `use` is widened to unknown so describe.each does not try to unify the six
// different hook result types into one.
const cases = [
  {
    name: 'useCalendar',
    use: (): unknown => useCalendar(),
    fetcher: () => getCalendar,
    value: { events: [] },
  },
  {
    name: 'useSpotifyStatus',
    use: (): unknown => useSpotifyStatus(),
    fetcher: () => getSpotifyStatus,
    value: { connected: false },
  },
  {
    name: 'useManualInterests',
    use: (): unknown => useManualInterests(),
    fetcher: () => listManualInterests,
    value: [],
  },
  {
    name: 'useSpotifyInterests',
    use: (): unknown => useSpotifyInterests(),
    fetcher: () => listSpotifyInterests,
    value: [],
  },
  { name: 'useMe', use: (): unknown => useMe(), fetcher: () => getMe, value: user },
  {
    name: 'useEvent',
    use: (): unknown => useEvent('e1'),
    fetcher: () => getEvent,
    value: { id: 'e1' },
  },
];

describe.each(cases)('$name', ({ use, fetcher, value }) => {
  it('stays idle while auth is still loading', async () => {
    vi.mocked(useAuth).mockReturnValue(authLoading);
    vi.mocked(fetcher()).mockResolvedValue(value as never);

    renderHook(use, { wrapper });
    await settle();

    expect(fetcher()).not.toHaveBeenCalled();
  });

  it('fetches exactly once across the loading -> authenticated transition', async () => {
    vi.mocked(useAuth).mockReturnValue(authLoading);
    vi.mocked(fetcher()).mockResolvedValue(value as never);

    const { rerender } = renderHook(use, { wrapper });
    await settle();

    vi.mocked(useAuth).mockReturnValue(authResolved);
    rerender();

    await waitFor(() => expect(fetcher()).toHaveBeenCalledTimes(1));
    await settle();
    expect(fetcher()).toHaveBeenCalledTimes(1);
  });
});
