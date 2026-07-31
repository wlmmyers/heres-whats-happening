import { describe, it, expect, vi, beforeEach } from 'vitest';

vi.mock('./client', () => ({
  apiFetch: vi.fn(),
}));

import { getCalendar, getCityCalendar } from './calendar';
import { apiFetch } from './client';

beforeEach(() => {
  vi.resetAllMocks();
});

describe('getCalendar', () => {
  it('fetches the first page without a cursor', async () => {
    const page = { events: [{ id: 'x' }] };
    (apiFetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce(page);
    const result = await getCalendar();
    expect(apiFetch).toHaveBeenCalledWith('/me/calendar');
    expect(result).toEqual(page);
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
  it('fetches the first page for a city without a cursor', async () => {
    const page = { events: [{ id: 'c1' }] };
    (apiFetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce(page);
    const result = await getCityCalendar('city-uuid');
    expect(apiFetch).toHaveBeenCalledWith('/calendar/city-uuid');
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
