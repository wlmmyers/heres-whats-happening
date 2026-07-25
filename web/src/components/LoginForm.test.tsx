import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));

import { useAuth } from '../auth/useAuth';
import LoginForm from './LoginForm';

beforeEach(() => {
  vi.resetAllMocks();
});

describe('LoginForm', () => {
  it('calls login and onSuccess on submit', async () => {
    const login = vi.fn().mockResolvedValueOnce(undefined);
    vi.mocked(useAuth).mockReturnValue({
      status: 'anonymous',
      user: null,
      login,
      signup: vi.fn(),
      logout: vi.fn(),
    });
    const onSuccess = vi.fn();
    render(
      <MemoryRouter>
        <LoginForm onSuccess={onSuccess} />
      </MemoryRouter>,
    );
    await userEvent.type(screen.getByLabelText(/email/i), 'a@x');
    await userEvent.type(screen.getByLabelText(/password/i), 'hunter22');
    await userEvent.click(screen.getByRole('button', { name: /sign in/i }));
    await waitFor(() => expect(login).toHaveBeenCalledWith('a@x', 'hunter22'));
    expect(onSuccess).toHaveBeenCalled();
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
