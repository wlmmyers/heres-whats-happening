import { apiFetch, setAccessToken, clearAccessToken } from './client';

export interface User {
  id: string;
  email: string;
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

export async function getMe(): Promise<User> {
  return apiFetch<User>('/me');
}
