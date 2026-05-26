import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, act } from '@testing-library/react';
import { AuthProvider } from './AuthContext';
import { useAuth } from './useAuth';

vi.mock('../api/auth', () => ({
  getMe: vi.fn(),
  login: vi.fn(),
  logout: vi.fn(),
  signup: vi.fn(),
}));

import * as authApi from '../api/auth';

beforeEach(() => {
  vi.resetAllMocks();
});

function Probe() {
  const { user, status } = useAuth();
  return (
    <div>
      <span data-testid="status">{status}</span>
      <span data-testid="email">{user?.email ?? ''}</span>
    </div>
  );
}

describe('AuthProvider', () => {
  it('boots to "loading" then "authenticated" when /me succeeds', async () => {
    (authApi.getMe as ReturnType<typeof vi.fn>).mockResolvedValueOnce({ id: 'u1', email: 'a@x' });
    render(
      <AuthProvider>
        <Probe />
      </AuthProvider>,
    );
    expect(screen.getByTestId('status').textContent).toBe('loading');
    await waitFor(() => {
      expect(screen.getByTestId('status').textContent).toBe('authenticated');
    });
    expect(screen.getByTestId('email').textContent).toBe('a@x');
  });

  it('boots to "anonymous" when /me fails', async () => {
    (authApi.getMe as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error('401'));
    render(
      <AuthProvider>
        <Probe />
      </AuthProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('status').textContent).toBe('anonymous');
    });
  });

  it('login() transitions to authenticated', async () => {
    (authApi.getMe as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error('401'));
    (authApi.login as ReturnType<typeof vi.fn>).mockResolvedValueOnce(undefined);
    (authApi.getMe as ReturnType<typeof vi.fn>).mockResolvedValueOnce({ id: 'u2', email: 'b@x' });

    let auth: ReturnType<typeof useAuth>;
    function Capture() {
      auth = useAuth();
      return <Probe />;
    }
    render(
      <AuthProvider>
        <Capture />
      </AuthProvider>,
    );
    await waitFor(() => expect(screen.getByTestId('status').textContent).toBe('anonymous'));
    await act(async () => {
      await auth!.login('b@x', 'pw');
    });
    await waitFor(() => expect(screen.getByTestId('status').textContent).toBe('authenticated'));
    expect(screen.getByTestId('email').textContent).toBe('b@x');
  });
});
