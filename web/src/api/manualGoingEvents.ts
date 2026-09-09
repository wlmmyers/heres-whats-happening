import { apiFetch } from './client';

// A show the user typed in themselves. Deliberately not a CalendarEvent: there
// is no events row behind it, so it has no id to open, no venue record, no
// score and no artist. `date` is a calendar day ("2026-03-10"), never an
// instant — parse it with parseLocalDate, not `new Date`.
export interface ManualGoingEvent {
  id: string;
  date: string;
  event_name: string;
  venue_name: string;
  created_at: string;
}

export type ManualGoingEventInput = Pick<ManualGoingEvent, 'date' | 'event_name' | 'venue_name'>;

export async function listManualGoingEvents(): Promise<ManualGoingEvent[]> {
  const out = await apiFetch<{ events: ManualGoingEvent[] }>('/me/manual-added-going-events');
  return out.events;
}

export async function createManualGoingEvent(
  input: ManualGoingEventInput,
): Promise<ManualGoingEvent> {
  return apiFetch<ManualGoingEvent>('/me/manual-added-going-events', {
    method: 'POST',
    body: input,
  });
}

export async function updateManualGoingEvent(
  id: string,
  input: ManualGoingEventInput,
): Promise<ManualGoingEvent> {
  return apiFetch<ManualGoingEvent>(`/me/manual-added-going-events/${id}`, {
    method: 'PUT',
    body: input,
  });
}

export async function deleteManualGoingEvent(id: string): Promise<void> {
  await apiFetch<void>(`/me/manual-added-going-events/${id}`, { method: 'DELETE' });
}
