import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));

import { useAuth } from '../auth/useAuth';
import SignupForm from './SignupForm';

beforeEach(() => {
  vi.resetAllMocks();
});

function mockSignup(signup: ReturnType<typeof vi.fn>) {
  vi.mocked(useAuth).mockReturnValue({
    status: 'anonymous',
    user: null,
    login: vi.fn(),
    signup,
    logout: vi.fn(),
    refreshUser: vi.fn(),
  });
}

describe('SignupForm', () => {
  it('shows a friendly message when rate limited', async () => {
    const err = Object.assign(new Error('too many requests, please try again later'), {
      code: 'rate_limited',
    });
    mockSignup(vi.fn().mockRejectedValueOnce(err));
    render(
      <MemoryRouter>
        <SignupForm />
      </MemoryRouter>,
    );
    await userEvent.type(screen.getByLabelText(/email/i), 'a@x.com');
    await userEvent.type(screen.getByLabelText(/password/i), 'hunter22');
    await userEvent.click(screen.getByRole('button', { name: /create account/i }));
    await waitFor(() =>
      expect(screen.getByText(/too many sign-ups from your network/i)).toBeInTheDocument(),
    );
  });
});
