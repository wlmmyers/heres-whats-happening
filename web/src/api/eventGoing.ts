import { apiFetch } from './client';
import type { CalendarEvent } from './calendar';

export async function markEventGoing(eventId: string): Promise<void> {
  await apiFetch<void>('/me/event-going', {
    method: 'POST',
    body: { event_id: eventId },
  });
}

export async function resetEventGoing(eventId: string): Promise<void> {
  await apiFetch<void>(`/me/event-going?event_id=${eventId}`, { method: 'DELETE' });
}

export async function listGoing(): Promise<string[]> {
  return apiFetch<string[]>('/me/event-going', { method: 'GET' });
}

// The same going list as listGoing, but as full event rows for rendering. The
// id-only call above stays for the toggle state on every card.
export async function listGoingEvents(): Promise<CalendarEvent[]> {
  return apiFetch<CalendarEvent[]>('/me/event-going/events', { method: 'GET' });
}
