import { apiFetch } from './client';

export interface Interest {
  id: string;
  value: string;
  normalized_value: string;
  weight: number;
  created_at: string;
}

export async function listManualInterests(): Promise<Interest[]> {
  const out = await apiFetch<{ interests: Interest[] }>('/me/manual-interests');
  return out.interests;
}

export async function createManualInterest(value: string): Promise<Interest> {
  return apiFetch<Interest>('/me/manual-interests', {
    method: 'POST',
    body: { value },
  });
}

export async function deleteManualInterest(id: string): Promise<void> {
  await apiFetch<void>(`/me/manual-interests/${id}`, { method: 'DELETE' });
}
