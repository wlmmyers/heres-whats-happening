import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router-dom';

vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));

import { useAuth } from '../auth/useAuth';
import Layout from './Layout';

function renderLayout() {
  return render(
    <MemoryRouter initialEntries={['/calendar']}>
      <Routes>
        <Route path="/" element={<Layout />}>
          <Route path="calendar" element={<div>cal</div>} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
});

describe('Layout', () => {
  it('shows nav and account menu when authenticated', () => {
    vi.mocked(useAuth).mockReturnValue({
      status: 'authenticated',
      user: { id: 'u', email: 'a@x' },
      login: vi.fn(),
      signup: vi.fn(),
      logout: vi.fn(),
    });
    renderLayout();
    expect(screen.getByRole('link', { name: /interests/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /account menu/i })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /^sign in$/i })).not.toBeInTheDocument();
  });

  it('shows sign in and sign up when anonymous', () => {
    vi.mocked(useAuth).mockReturnValue({
      status: 'anonymous',
      user: null,
      login: vi.fn(),
      signup: vi.fn(),
      logout: vi.fn(),
    });
    renderLayout();
    expect(screen.getByRole('link', { name: /sign in/i })).toHaveAttribute('href', '/login');
    expect(screen.getByRole('link', { name: /sign up/i })).toHaveAttribute('href', '/signup');
    expect(screen.queryByRole('link', { name: /interests/i })).not.toBeInTheDocument();
  });
});
