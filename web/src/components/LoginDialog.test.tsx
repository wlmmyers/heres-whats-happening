import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));

import { useAuth } from '../auth/useAuth';
import LoginDialog from './LoginDialog';

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(useAuth).mockReturnValue({
    status: 'anonymous',
    user: null,
    login: vi.fn(),
    signup: vi.fn(),
    logout: vi.fn(),
    refreshUser: vi.fn(),
  });
});

describe('LoginDialog', () => {
  it('renders a sign-in dialog with the login form', () => {
    render(
      <MemoryRouter>
        <LoginDialog />
      </MemoryRouter>,
    );
    expect(screen.getByRole('dialog', { name: /sign in/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /sign in/i })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /sign up/i })).toHaveAttribute('href', '/signup');
  });
});
