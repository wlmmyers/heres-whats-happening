import { apiFetch } from './client';

export async function markNotInterested(eventId: string): Promise<void> {
  await apiFetch<void>('/me/not-interested', { method: 'POST', body: { event_id: eventId } });
}

export async function resetNotInterested(): Promise<void> {
  await apiFetch<void>('/me/not-interested', { method: 'DELETE' });
}
