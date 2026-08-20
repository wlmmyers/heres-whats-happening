import { apiFetch } from './client';

export async function updateShowSetlists(showSetlists: boolean): Promise<void> {
  await apiFetch<void>('/me/show-setlists', {
    method: 'PATCH',
    body: { show_setlists: showSetlists },
  });
}
