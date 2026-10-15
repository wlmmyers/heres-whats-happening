import { apiFetch, setAccessToken, clearAccessToken } from './client';

export interface User {
  id: string;
  email: string;
  city_id: string;
  confirmed: boolean;
  score_threshold?: number;
  show_setlists: boolean;
}

interface AuthSuccess {
  access_token: string;
  user?: User;
}

export async function signup(email: string, password: string): Promise<User> {
  const out = await apiFetch<AuthSuccess>('/auth/signup', {
    method: 'POST',
    body: { email, password },
  });
  setAccessToken(out.access_token);
  if (!out.user) throw new Error('signup returned no user');
  return out.user;
}

export async function login(email: string, password: string): Promise<void> {
  const out = await apiFetch<AuthSuccess>('/auth/login', {
    method: 'POST',
    body: { email, password },
  });
  setAccessToken(out.access_token);
}

export async function logout(): Promise<void> {
  try {
    await apiFetch<void>('/auth/logout', { method: 'POST' });
  } finally {
    clearAccessToken();
  }
}

/**
 * Permanently deletes the signed-in account and everything stored for it. The
 * token is only dropped once the delete succeeds: on a failure the account is
 * still there, and the user should stay signed in to it.
 */
export async function deleteAccount(): Promise<void> {
  await apiFetch<void>('/me', { method: 'DELETE' });
  clearAccessToken();
}

export async function getMe(): Promise<User> {
  return apiFetch<User>('/me');
}

/** Mints a fresh confirmation link and mails it, invalidating the previous one. */
export async function resendConfirmation(): Promise<void> {
  await apiFetch<void>('/auth/confirm/resend', { method: 'POST' });
}

/**
 * Asks for a reset link to be mailed to `email`. Resolves the same way whether
 * or not an account exists for the address — the API does not say.
 */
export async function requestPasswordReset(email: string): Promise<void> {
  await apiFetch<void>('/auth/password-reset/request', {
    method: 'POST',
    body: { email },
  });
}

/**
 * Spends an emailed reset token to set a new password, signing the account out
 * everywhere. Resolves with the account's email so the sign-in form can be
 * prefilled.
 */
export async function resetPassword(token: string, password: string): Promise<string> {
  const out = await apiFetch<{ email: string }>('/auth/password-reset', {
    method: 'POST',
    body: { token, password },
  });
  return out.email;
}
