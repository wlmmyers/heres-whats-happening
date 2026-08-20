import { describe, it, expect, vi, beforeEach } from 'vitest';

vi.mock('./client', () => ({ apiFetch: vi.fn() }));

import { apiFetch } from './client';
import { updateShowSetlists } from './showSetlists';

beforeEach(() => {
  vi.resetAllMocks();
});

describe('showSetlists api', () => {
  it('PATCHes /me/show-setlists to opt in', async () => {
    (apiFetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce(undefined);
    await updateShowSetlists(true);
    expect(apiFetch).toHaveBeenCalledWith('/me/show-setlists', {
      method: 'PATCH',
      body: { show_setlists: true },
    });
  });

  it('sends an explicit false when opting back out', async () => {
    (apiFetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce(undefined);
    await updateShowSetlists(false);
    expect(apiFetch).toHaveBeenCalledWith('/me/show-setlists', {
      method: 'PATCH',
      body: { show_setlists: false },
    });
  });
});
