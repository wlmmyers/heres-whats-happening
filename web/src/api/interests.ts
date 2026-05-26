import { apiFetch } from './client';

export interface Interest {
  id: string;
  value: string;
  normalized_value: string;
  weight: number;
  created_at: string;
}

export async function listInterests(): Promise<Interest[]> {
  const out = await apiFetch<{ interests: Interest[] }>('/me/interests');
  return out.interests;
}

export async function createInterest(value: string): Promise<Interest> {
  return apiFetch<Interest>('/me/interests', { method: 'POST', body: { value } });
}

export async function deleteInterest(id: string): Promise<void> {
  await apiFetch<void>(`/me/interests/${id}`, { method: 'DELETE' });
}
