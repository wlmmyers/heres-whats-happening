import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Routes, Route } from 'react-router-dom';

vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));
vi.mock('../api/auth', () => ({ resendConfirmation: vi.fn() }));

import { useAuth } from '../auth/useAuth';
import { resendConfirmation } from '../api/auth';
import Layout from './Layout';

function mockAuth(status: 'authenticated' | 'anonymous') {
  vi.mocked(useAuth).mockReturnValue({
    status,
    user:
      status === 'authenticated'
        ? { id: 'u1', email: 'a@x.com', city_id: 'city-1', confirmed: true, show_setlists: false }
        : null,
    login: vi.fn(),
    signup: vi.fn(),
    logout: vi.fn(),
    refreshUser: vi.fn(),
  });
}

function renderLayoutAt(entry: string) {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <Routes>
        <Route path="/" element={<Layout />}>
          <Route index element={<div>home</div>} />
          <Route path="login" element={<div>login</div>} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  mockAuth('authenticated');
});

describe('confirmation modals', () => {
  it('shows the welcome modal on ?welcome=true', () => {
    renderLayoutAt('/?welcome=true');
    expect(screen.getByRole('dialog', { name: /welcome/i })).toBeInTheDocument();
  });

  it('shows no modal without the params', () => {
    renderLayoutAt('/');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  // Confirming on a phone with no session is a real case, but there the login
  // card carries the "your email is confirmed" copy itself (see LoginDialog), so
  // the modal would only stack on top of it.
  it('leaves the welcome message to the login card for an anonymous visitor', () => {
    mockAuth('anonymous');
    renderLayoutAt('/login?welcome=true');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('dismisses the welcome modal and clears the param', async () => {
    renderLayoutAt('/?welcome=true');
    await userEvent.click(screen.getByRole('button', { name: /got it/i }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('shows the error modal on ?confirmerror=true and offers a fresh link', async () => {
    vi.mocked(resendConfirmation).mockResolvedValue(undefined);
    renderLayoutAt('/?confirmerror=true');

    expect(screen.getByRole('dialog', { name: /link/i })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: /send a new link/i }));
    expect(resendConfirmation).toHaveBeenCalledTimes(1);
  });

  it('asks an anonymous visitor to sign in rather than offering resend', () => {
    mockAuth('anonymous');
    renderLayoutAt('/login?confirmerror=true');

    expect(screen.getByRole('dialog', { name: /link/i })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /send a new link/i })).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: /sign in/i })).toBeInTheDocument();
  });
});
