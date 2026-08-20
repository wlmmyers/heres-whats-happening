import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import type { ReactElement } from 'react';

vi.mock('./useAuth', () => ({ useAuth: vi.fn() }));

import { useAuth } from './useAuth';
import RequireAuth from './RequireAuth';

function mockAuth(status: 'loading' | 'authenticated' | 'anonymous', confirmed = true) {
  vi.mocked(useAuth).mockReturnValue({
    status,
    user:
      status === 'authenticated'
        ? { id: 'u1', email: 'a@x.com', city_id: 'city-1', confirmed, show_setlists: false }
        : null,
    login: vi.fn(),
    signup: vi.fn(),
    logout: vi.fn(),
    refreshUser: vi.fn(),
  });
}

function renderAt(element: ReactElement) {
  return render(
    <MemoryRouter initialEntries={['/protected']}>
      <Routes>
        <Route path="/protected" element={element} />
        <Route path="/login" element={<div>login page</div>} />
        <Route path="/confirm-email" element={<div>confirm page</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => vi.resetAllMocks());

describe('RequireAuth', () => {
  it('renders children for a confirmed user', () => {
    mockAuth('authenticated', true);
    renderAt(
      <RequireAuth>
        <div>secret</div>
      </RequireAuth>,
    );
    expect(screen.getByText('secret')).toBeInTheDocument();
  });

  it('sends an anonymous user to login', () => {
    mockAuth('anonymous');
    renderAt(
      <RequireAuth>
        <div>secret</div>
      </RequireAuth>,
    );
    expect(screen.getByText('login page')).toBeInTheDocument();
  });

  it('sends an unconfirmed user to /confirm-email', () => {
    mockAuth('authenticated', false);
    renderAt(
      <RequireAuth>
        <div>secret</div>
      </RequireAuth>,
    );
    expect(screen.getByText('confirm page')).toBeInTheDocument();
    expect(screen.queryByText('secret')).not.toBeInTheDocument();
  });

  it('lets an unconfirmed user through when allowUnconfirmed is set', () => {
    mockAuth('authenticated', false);
    renderAt(
      <RequireAuth allowUnconfirmed>
        <div>secret</div>
      </RequireAuth>,
    );
    expect(screen.getByText('secret')).toBeInTheDocument();
  });
});
