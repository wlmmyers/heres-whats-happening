import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import { AuthProvider } from '../auth/AuthContext';
import LoginPage from './LoginPage';

vi.mock('../api/auth', () => ({
  getMe: vi.fn().mockRejectedValue(new Error('401')),
  login: vi.fn(),
  logout: vi.fn(),
  signup: vi.fn(),
}));

import * as authApi from '../api/auth';

beforeEach(() => {
  vi.resetAllMocks();
  (authApi.getMe as ReturnType<typeof vi.fn>).mockRejectedValue(new Error('401'));
});

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/login']}>
      <AuthProvider>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/calendar" element={<div>calendar-route</div>} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  );
}

describe('LoginPage', () => {
  it('submits credentials and redirects on success', async () => {
    (authApi.login as ReturnType<typeof vi.fn>).mockResolvedValueOnce(undefined);
    (authApi.getMe as ReturnType<typeof vi.fn>).mockResolvedValueOnce({ id: 'u', email: 'a@x' });
    renderPage();
    await userEvent.type(screen.getByLabelText(/email/i), 'a@x');
    await userEvent.type(screen.getByLabelText(/password/i), 'hunter22');
    await userEvent.click(screen.getByRole('button', { name: /sign in/i }));
    await waitFor(() => {
      expect(screen.getByText(/calendar-route/i)).toBeInTheDocument();
    });
    expect(authApi.login).toHaveBeenCalledWith('a@x', 'hunter22');
  });

  it('renders error message on failure', async () => {
    const err = Object.assign(new Error('Invalid'), { status: 401, code: 'invalid_credentials' });
    (authApi.login as ReturnType<typeof vi.fn>).mockRejectedValueOnce(err);
    renderPage();
    await userEvent.type(screen.getByLabelText(/email/i), 'a@x');
    await userEvent.type(screen.getByLabelText(/password/i), 'wrong');
    await userEvent.click(screen.getByRole('button', { name: /sign in/i }));
    await waitFor(() => {
      expect(screen.getByText(/email or password is wrong|invalid/i)).toBeInTheDocument();
    });
  });
});
