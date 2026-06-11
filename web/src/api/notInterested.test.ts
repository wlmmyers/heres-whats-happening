import { describe, it, expect, vi, beforeEach } from 'vitest';

vi.mock('./client', () => ({ apiFetch: vi.fn() }));

import { apiFetch } from './client';
import { markNotInterested, resetNotInterested } from './notInterested';

beforeEach(() => {
  vi.resetAllMocks();
});

describe('notInterested api', () => {
  it('POSTs the event id to /me/not-interested', async () => {
    (apiFetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce(undefined);
    await markNotInterested('e1');
    expect(apiFetch).toHaveBeenCalledWith('/me/not-interested', {
      method: 'POST',
      body: { event_id: 'e1' },
    });
  });

  it('DELETEs /me/not-interested to reset the list', async () => {
    (apiFetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce(undefined);
    await resetNotInterested();
    expect(apiFetch).toHaveBeenCalledWith('/me/not-interested', { method: 'DELETE' });
  });
});
