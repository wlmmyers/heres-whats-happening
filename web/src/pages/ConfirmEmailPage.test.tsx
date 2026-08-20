import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));
vi.mock('../api/auth', () => ({ resendConfirmation: vi.fn() }));

const navigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom');
  return { ...actual, useNavigate: () => navigate };
});

import { useAuth } from '../auth/useAuth';
import { resendConfirmation } from '../api/auth';
import ConfirmEmailPage from './ConfirmEmailPage';

function mockAuth(overrides: Partial<ReturnType<typeof useAuth>> = {}) {
  vi.mocked(useAuth).mockReturnValue({
    status: 'authenticated',
    user: {
      id: 'u1',
      email: 'someone@example.com',
      city_id: 'city-1',
      confirmed: false,
      show_setlists: false,
    },
    login: vi.fn(),
    signup: vi.fn(),
    logout: vi.fn(),
    refreshUser: vi.fn(),
    ...overrides,
  });
}

function renderPage() {
  return render(
    <MemoryRouter>
      <ConfirmEmailPage />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  mockAuth();
});

describe('ConfirmEmailPage', () => {
  it('names the address the mail went to', () => {
    renderPage();
    expect(screen.getByText(/someone@example.com/)).toBeInTheDocument();
  });

  it('resends on click and reports that it sent', async () => {
    vi.mocked(resendConfirmation).mockResolvedValue(undefined);
    renderPage();

    await userEvent.click(screen.getByRole('button', { name: /resend/i }));

    expect(resendConfirmation).toHaveBeenCalledTimes(1);
    // Scoped to the live region: the body copy also contains "sent".
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent(/sent/i));
  });

  it('reports the rate limit instead of a generic failure', async () => {
    vi.mocked(resendConfirmation).mockRejectedValue({ status: 429, code: 'rate_limited' });
    renderPage();

    await userEvent.click(screen.getByRole('button', { name: /resend/i }));

    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent(/too many/i));
  });

  it('moves a stale tab along when the window regains focus after confirming elsewhere', async () => {
    const refreshUser = vi
      .fn()
      .mockResolvedValue({ id: 'u1', email: 'someone@example.com', confirmed: true });
    mockAuth({ refreshUser });
    renderPage();

    window.dispatchEvent(new Event('focus'));

    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/calendar', { replace: true }));
  });

  it('stays put when the recheck finds the account still unconfirmed', async () => {
    const refreshUser = vi
      .fn()
      .mockResolvedValue({ id: 'u1', email: 'someone@example.com', confirmed: false });
    mockAuth({ refreshUser });
    renderPage();

    window.dispatchEvent(new Event('focus'));

    await waitFor(() => expect(refreshUser).toHaveBeenCalled());
    expect(navigate).not.toHaveBeenCalled();
  });

  it('offers a way out', () => {
    renderPage();
    expect(screen.getByRole('button', { name: /sign out/i })).toBeInTheDocument();
  });
});
