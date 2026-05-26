import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import { AuthProvider } from '../auth/AuthContext';
import SignupPage from './SignupPage';

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

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/signup']}>
      <AuthProvider>
        <Routes>
          <Route path="/signup" element={<SignupPage />} />
          <Route path="/onboarding" element={<div>onboarding-route</div>} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  );
}

describe('SignupPage', () => {
  it('signs up and redirects to onboarding', async () => {
    (authApi.getMe as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error('401'));
    (authApi.signup as ReturnType<typeof vi.fn>).mockResolvedValueOnce({ id: 'u', email: 'new@x' });

    renderPage();
    await userEvent.type(screen.getByLabelText(/email/i), 'new@x');
    await userEvent.type(screen.getByLabelText(/password/i), 'hunter22');
    await userEvent.click(screen.getByRole('button', { name: /create account/i }));
    await waitFor(() => expect(screen.getByText(/onboarding-route/)).toBeInTheDocument());
    expect(authApi.signup).toHaveBeenCalledWith('new@x', 'hunter22');
  });

  it('shows error on duplicate email', async () => {
    (authApi.getMe as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error('401'));
    const err = Object.assign(new Error('email taken'), { status: 409, code: 'email_taken' });
    (authApi.signup as ReturnType<typeof vi.fn>).mockRejectedValueOnce(err);

    renderPage();
    await userEvent.type(screen.getByLabelText(/email/i), 'dup@x');
    await userEvent.type(screen.getByLabelText(/password/i), 'hunter22');
    await userEvent.click(screen.getByRole('button', { name: /create account/i }));
    await waitFor(() => expect(screen.getByText(/an account with that email already exists/i)).toBeInTheDocument());
  });
});
