import { apiFetch } from './client';

// Allowed slider range (fractions of 1). Kept in sync with the backend
// validation in internal/http/handlers/match_threshold.go.
export const MIN_THRESHOLD = 0.2;
export const MAX_THRESHOLD = 0.6;

export async function updateMatchThreshold(threshold: number): Promise<void> {
  await apiFetch<{ status: string }>('/me/match-threshold', {
    method: 'PATCH',
    body: { threshold },
  });
}
