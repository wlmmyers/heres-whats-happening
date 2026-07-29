import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Routes, Route } from 'react-router-dom';

vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));

import { useAuth } from '../auth/useAuth';
import LoginForm from './LoginForm';

beforeEach(() => {
  vi.resetAllMocks();
});

describe('LoginForm', () => {
  it('calls login and navigates to the calendar on submit', async () => {
    const login = vi.fn().mockResolvedValueOnce(undefined);
    vi.mocked(useAuth).mockReturnValue({
      status: 'anonymous',
      user: null,
      login,
      signup: vi.fn(),
      logout: vi.fn(),
      refreshUser: vi.fn(),
    });
    render(
      <MemoryRouter initialEntries={['/login']}>
        <Routes>
          <Route path="/login" element={<LoginForm />} />
          <Route path="/calendar/seattle" element={<div>calendar</div>} />
        </Routes>
      </MemoryRouter>,
    );
    await userEvent.type(screen.getByLabelText(/email/i), 'a@x');
    await userEvent.type(screen.getByLabelText(/password/i), 'hunter22');
    await userEvent.click(screen.getByRole('button', { name: /sign in/i }));
    await waitFor(() => expect(login).toHaveBeenCalledWith('a@x', 'hunter22'));
    expect(await screen.findByText('calendar')).toBeInTheDocument();
  });

  // An anonymous visitor confirming on another device lands here rather than on
  // the welcome modal, so the confirmation copy has to live in the card itself.
  it('greets a just-confirmed visitor and prefills the email from the link', () => {
    vi.mocked(useAuth).mockReturnValue({
      status: 'anonymous',
      user: null,
      login: vi.fn(),
      signup: vi.fn(),
      logout: vi.fn(),
      refreshUser: vi.fn(),
    });
    render(
      <MemoryRouter initialEntries={['/login?welcome=true&email=a%40x.com']}>
        <LoginForm />
      </MemoryRouter>,
    );
    expect(screen.getByText(/your email is confirmed/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/email/i)).toHaveValue('a@x.com');
  });

  it('shows an error when credentials are invalid', async () => {
    const err = Object.assign(new Error('Invalid'), { code: 'invalid_credentials' });
    const login = vi.fn().mockRejectedValueOnce(err);
    vi.mocked(useAuth).mockReturnValue({
      status: 'anonymous',
      user: null,
      login,
      signup: vi.fn(),
      logout: vi.fn(),
      refreshUser: vi.fn(),
    });
    render(
      <MemoryRouter>
        <LoginForm />
      </MemoryRouter>,
    );
    await userEvent.type(screen.getByLabelText(/email/i), 'a@x');
    await userEvent.type(screen.getByLabelText(/password/i), 'wrong');
    await userEvent.click(screen.getByRole('button', { name: /sign in/i }));
    await waitFor(() =>
      expect(screen.getByText(/email or password is wrong/i)).toBeInTheDocument(),
    );
  });

  it('shows a friendly message when rate limited', async () => {
    const err = Object.assign(new Error('too many requests, please try again later'), {
      code: 'rate_limited',
    });
    const login = vi.fn().mockRejectedValueOnce(err);
    vi.mocked(useAuth).mockReturnValue({
      status: 'anonymous',
      user: null,
      login,
      signup: vi.fn(),
      logout: vi.fn(),
      refreshUser: vi.fn(),
    });
    render(
      <MemoryRouter>
        <LoginForm />
      </MemoryRouter>,
    );
    await userEvent.type(screen.getByLabelText(/email/i), 'a@x');
    await userEvent.type(screen.getByLabelText(/password/i), 'hunter22');
    await userEvent.click(screen.getByRole('button', { name: /sign in/i }));
    await waitFor(() => expect(screen.getByText(/too many login attempts/i)).toBeInTheDocument());
  });
});
