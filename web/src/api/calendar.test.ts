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
  it('returns bundled logged-out data without calling the API when loggedOut', async () => {
    const events = await getCalendar('2026-01-01', '2026-12-31', true);
    expect(events.length).toBeGreaterThan(0);
    expect(events[0]).toHaveProperty('id');
    expect(apiFetch).not.toHaveBeenCalled();
  });

  it('fetches from the API when not logged out', async () => {
    (apiFetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({ events: [{ id: 'x' }] });
    const events = await getCalendar('2026-01-01', '2026-12-31', false);
    expect(apiFetch).toHaveBeenCalledWith('/me/calendar?from=2026-01-01&to=2026-12-31');
    expect(events).toEqual([{ id: 'x' }]);
  });
});
