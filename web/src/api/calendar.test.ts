import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

vi.mock('./client', () => ({
  apiFetch: vi.fn(),
}));

import { getCalendar, getCityCalendar } from './calendar';
import { apiFetch } from './client';

// Uncursored pages send a starts_at lower bound of local midnight today, which
// the server parses with time.Parse(time.RFC3339, raw) — hence the full
// timestamp rather than a bare date. vitest.config.ts pins TZ to
// America/Los_Angeles, so midnight local on Jun 15 is 07:00Z.
const MIDNIGHT_JUN_15 = 'starts_at=2026-06-15T07%3A00%3A00.000Z';

beforeEach(() => {
  vi.resetAllMocks();
  vi.useFakeTimers();
  vi.setSystemTime(new Date('2026-06-15T18:30:00Z')); // 11:30 local
});

afterEach(() => {
  vi.useRealTimers();
});

describe('getCalendar', () => {
  it('fetches the first page from local midnight today when there is no cursor', async () => {
    const page = { events: [{ id: 'x' }] };
    (apiFetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce(page);
    const result = await getCalendar();
    expect(apiFetch).toHaveBeenCalledWith(`/me/calendar?${MIDNIGHT_JUN_15}`);
    expect(result).toEqual(page);
  });

  it('uses the local day, not the UTC day, once UTC has rolled over', async () => {
    // 22:30 local on Jun 15, by which point UTC is already on Jun 16.
    vi.setSystemTime(new Date('2026-06-16T05:30:00Z'));
    (apiFetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({ events: [] });
    await getCalendar();
    expect(apiFetch).toHaveBeenCalledWith(`/me/calendar?${MIDNIGHT_JUN_15}`);
  });

  it('passes the cursor through as a query param', async () => {
    const page = { events: [{ id: 'y' }], next_cursor: 'cur-2' };
    (apiFetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce(page);
    const result = await getCalendar('cur-1');
    expect(apiFetch).toHaveBeenCalledWith('/me/calendar?cursor=cur-1');
    expect(result).toEqual(page);
  });

  it('encodes cursors containing URL-unsafe characters', async () => {
    (apiFetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({ events: [] });
    await getCalendar('a+b/c=');
    expect(apiFetch).toHaveBeenCalledWith('/me/calendar?cursor=a%2Bb%2Fc%3D');
  });
});

describe('getCityCalendar', () => {
  it('fetches the first page for a city from local midnight today', async () => {
    const page = { events: [{ id: 'c1' }] };
    (apiFetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce(page);
    const result = await getCityCalendar('city-uuid');
    expect(apiFetch).toHaveBeenCalledWith(`/calendar/city-uuid?${MIDNIGHT_JUN_15}`);
    expect(result).toEqual(page);
  });

  it('passes the cursor through as a query param', async () => {
    const page = { events: [{ id: 'c2' }], next_cursor: 'cur-2' };
    (apiFetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce(page);
    const result = await getCityCalendar('city-uuid', 'cur-1');
    expect(apiFetch).toHaveBeenCalledWith('/calendar/city-uuid?cursor=cur-1');
    expect(result).toEqual(page);
  });
});
