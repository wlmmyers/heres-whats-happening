import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Routes, Route, useLocation } from 'react-router-dom';

vi.mock('../api/auth', () => ({ resetPassword: vi.fn() }));
vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));

import { resetPassword } from '../api/auth';
import { useAuth } from '../auth/useAuth';
import ResetPasswordDialog from './ResetPasswordDialog';

const logout = vi.fn();

function mockAuth(status: 'anonymous' | 'authenticated') {
  vi.mocked(useAuth).mockReturnValue({
    status,
    user: null,
    login: vi.fn(),
    signup: vi.fn(),
    logout,
    refreshUser: vi.fn(),
    deleteAccount: vi.fn(),
  });
}

beforeEach(() => {
  vi.resetAllMocks();
  logout.mockResolvedValue(undefined);
  mockAuth('anonymous');
});

function LocationProbe() {
  const loc = useLocation();
  return <div data-testid="location">{loc.pathname + loc.search}</div>;
}

function renderAt(entry: string) {
  render(
    <MemoryRouter initialEntries={[entry]}>
      <Routes>
        <Route path="/reset-password" element={<ResetPasswordDialog />} />
        <Route path="/login" element={<div>login page</div>} />
      </Routes>
      <LocationProbe />
    </MemoryRouter>,
  );
}

async function fillAndSubmit(password: string, confirm = password) {
  await userEvent.type(screen.getByLabelText(/^new password/i), password);
  await userEvent.type(screen.getByLabelText(/confirm new password/i), confirm);
  await userEvent.click(screen.getByRole('button', { name: /reset password/i }));
}

describe('ResetPasswordDialog', () => {
  it('moves the token out of the address bar', async () => {
    renderAt('/reset-password?token=tok123');
    await waitFor(() =>
      expect(screen.getByTestId('location')).toHaveTextContent(/^\/reset-password$/),
    );
    expect(screen.getByRole('button', { name: /reset password/i })).toBeInTheDocument();
  });

  it('resets with the token and sends the user to a prefilled sign-in', async () => {
    vi.mocked(resetPassword).mockResolvedValueOnce('a+b@x.com');
    renderAt('/reset-password?token=tok123');
    await fillAndSubmit('newpassword');

    expect(resetPassword).toHaveBeenCalledWith('tok123', 'newpassword');
    expect(await screen.findByText('login page')).toBeInTheDocument();
    expect(screen.getByTestId('location')).toHaveTextContent(
      '/login?reset=true&email=a%2Bb%40x.com',
    );
    expect(logout).not.toHaveBeenCalled();
  });

  it('signs out a tab that was signed in, since the reset revoked its session', async () => {
    mockAuth('authenticated');
    vi.mocked(resetPassword).mockResolvedValueOnce('a@x.com');
    renderAt('/reset-password?token=tok123');
    await fillAndSubmit('newpassword');
    expect(await screen.findByText('login page')).toBeInTheDocument();
    expect(logout).toHaveBeenCalled();
  });

  it('rejects mismatched passwords without calling the API', async () => {
    renderAt('/reset-password?token=tok123');
    await fillAndSubmit('newpassword', 'different1');
    expect(await screen.findByText(/passwords don’t match/i)).toBeInTheDocument();
    expect(resetPassword).not.toHaveBeenCalled();
  });

  it('offers a new link when the token is invalid or expired', async () => {
    vi.mocked(resetPassword).mockRejectedValueOnce(
      Object.assign(new Error('bad'), { code: 'invalid_token' }),
    );
    renderAt('/reset-password?token=stale');
    await fillAndSubmit('newpassword');
    expect(await screen.findByText(/invalid or has expired/i)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /send a new link/i })).toHaveAttribute(
      'href',
      '/forgot-password',
    );
  });

  it('offers a new link when opened with no token at all', () => {
    renderAt('/reset-password');
    expect(screen.getByText(/invalid or has expired/i)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /reset password/i })).not.toBeInTheDocument();
  });
});
