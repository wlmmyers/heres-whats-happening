import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Routes, Route, useLocation } from 'react-router-dom';
import { AuthProvider } from '../auth/AuthContext';
import LandingPage from './LandingPage';

vi.mock('../api/auth', () => ({
  getMe: vi.fn(),
  login: vi.fn(),
  logout: vi.fn(),
  signup: vi.fn(),
}));

import * as authApi from '../api/auth';
import LoginDialog from '../components/LoginDialog';
import SignupDialog from '../components/SignupDialog';
import userEvent from '@testing-library/user-event';

beforeEach(() => {
  vi.resetAllMocks();
  // AuthProvider's mount call rejects → the landing page boots anonymous.
  (authApi.getMe as ReturnType<typeof vi.fn>).mockRejectedValue(new Error('401'));
});

function renderPage(children: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/']}>
        <AuthProvider>
          <Routes>
            <Route path="/" element={<LandingPage>{children}</LandingPage>} />
            <Route path="/calendar/seattle" element={<div>calendar-route</div>} />
            <Route path="/interests" element={<div>interests-route</div>} />
            <Route path="/confirm-email" element={<div>confirm-email-route</div>} />
          </Routes>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('LandingPage - Login', () => {
  it('renders matched events', async () => {
    renderPage(<LoginDialog />);
    await waitFor(() =>
      expect(screen.getAllByText('Tame Impala - The Deadbeat Tour').length).toBeGreaterThan(0),
    );
  });

  it('shows the login dialog and non-interactive events when logged out', async () => {
    renderPage(<LoginDialog />);
    await waitFor(() =>
      expect(screen.getByRole('dialog', { name: /sign in/i })).toBeInTheDocument(),
    );
  });

  it('submits credentials and redirects on success', async () => {
    // AuthProvider's mount call rejects → boots anonymous.
    (authApi.getMe as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error('401'));
    (authApi.login as ReturnType<typeof vi.fn>).mockResolvedValueOnce(undefined);
    // useAuth().login internally calls getMe again — return the user this time.
    (authApi.getMe as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      id: 'u',
      email: 'a@x',
    });

    renderPage(<LoginDialog />);
    await userEvent.type(screen.getByLabelText(/email/i), 'a@x');
    await userEvent.type(screen.getByLabelText(/password/i), 'hunter22');
    await userEvent.click(screen.getByRole('button', { name: /sign in/i }));
    await waitFor(() => {
      expect(screen.getByText(/calendar-route/i)).toBeInTheDocument();
    });
    expect(authApi.login).toHaveBeenCalledWith('a@x', 'hunter22');
  });

  it('renders error message on failure', async () => {
    (authApi.getMe as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error('401'));
    const err = Object.assign(new Error('Invalid'), {
      status: 401,
      code: 'invalid_credentials',
    });
    (authApi.login as ReturnType<typeof vi.fn>).mockRejectedValueOnce(err);

    renderPage(<LoginDialog />);
    await userEvent.type(screen.getByLabelText(/email/i), 'a@x');
    await userEvent.type(screen.getByLabelText(/password/i), 'wrong');
    await userEvent.click(screen.getByRole('button', { name: /sign in/i }));
    await waitFor(() => {
      expect(screen.getByText(/email or password is wrong/i)).toBeInTheDocument();
    });
  });
});

describe('LandingPage - Signup', () => {
  // A new account is unconfirmed, so it goes to /confirm-email rather than
  // straight into the product.
  it('signs up and redirects to confirm-email', async () => {
    (authApi.getMe as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error('401'));
    (authApi.signup as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      id: 'u',
      email: 'new@x',
      confirmed: false,
    });

    renderPage(<SignupDialog />);
    await userEvent.type(screen.getByLabelText(/email/i), 'new@x');
    await userEvent.type(screen.getByLabelText(/password/i), 'hunter22');
    await userEvent.click(screen.getByRole('button', { name: /create account/i }));
    await waitFor(() => expect(screen.getByText(/confirm-email-route/)).toBeInTheDocument());
    expect(authApi.signup).toHaveBeenCalledWith('new@x', 'hunter22');
  });

  it('shows error on duplicate email', async () => {
    (authApi.getMe as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error('401'));
    const err = Object.assign(new Error('email taken'), {
      status: 409,
      code: 'email_taken',
    });
    (authApi.signup as ReturnType<typeof vi.fn>).mockRejectedValueOnce(err);

    renderPage(<SignupDialog />);
    await userEvent.type(screen.getByLabelText(/email/i), 'dup@x');
    await userEvent.type(screen.getByLabelText(/password/i), 'hunter22');
    await userEvent.click(screen.getByRole('button', { name: /create account/i }));
    await waitFor(() =>
      expect(screen.getByText(/an account with that email already exists/i)).toBeInTheDocument(),
    );
  });
});

// The index route redirects, and ?welcome=true / ?confirmerror=true arrive
// there. Layout's modals read those params, so dropping them on the redirect
// loses the modal before it can ever render.
function LocationProbe() {
  const { search } = useLocation();
  return <div data-testid="search">{search}</div>;
}

describe('LandingPage - redirect preserves query params', () => {
  it('keeps the query string when redirecting an anonymous visitor to login', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/?welcome=true']}>
          <AuthProvider>
            <Routes>
              <Route path="/" element={<LandingPage />} />
              <Route path="/login" element={<LocationProbe />} />
            </Routes>
          </AuthProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    await waitFor(() => expect(screen.getByTestId('search')).toHaveTextContent('?welcome=true'));
  });
});
