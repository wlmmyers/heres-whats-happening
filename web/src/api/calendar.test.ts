import { describe, it, expect, vi, beforeEach } from 'vitest';

vi.mock('./client', () => ({
  apiFetch: vi.fn(),
}));

import { getCalendar } from './calendar';
import { apiFetch } from './client';

beforeEach(() => {
  vi.resetAllMocks();
});

describe('getCalendar', () => {
  it('fetches from the API', async () => {
    (apiFetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({ events: [{ id: 'x' }] });
    const events = await getCalendar('2026-01-01', '2026-12-31');
    expect(apiFetch).toHaveBeenCalledWith('/me/calendar?from=2026-01-01&to=2026-12-31');
    expect(events).toEqual([{ id: 'x' }]);
  });
});
