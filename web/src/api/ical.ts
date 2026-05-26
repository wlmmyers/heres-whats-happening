import { apiFetch } from './client';

export interface IcalToken {
  url: string;
}

export async function createIcalToken(): Promise<IcalToken> {
  return apiFetch<IcalToken>('/me/ical-token', { method: 'POST' });
}

export async function revokeIcalToken(): Promise<void> {
  await apiFetch<void>('/me/ical-token', { method: 'DELETE' });
}
