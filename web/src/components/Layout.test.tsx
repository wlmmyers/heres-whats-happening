import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router-dom';

vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));
vi.mock('../hooks/useScreenSize', () => ({ useScreenSize: vi.fn() }));

import { useAuth } from '../auth/useAuth';
import { useScreenSize } from '../hooks/useScreenSize';
import Layout from './Layout';

function mockAuthed() {
  vi.mocked(useAuth).mockReturnValue({
    status: 'authenticated',
    user: { id: 'u', email: 'a@x', city_id: 'city-1', confirmed: true, show_setlists: false },
    login: vi.fn(),
    signup: vi.fn(),
    logout: vi.fn(),
    refreshUser: vi.fn(),
    deleteAccount: vi.fn(),
  });
}

function mockPhone(isPhoneWidth: boolean) {
  vi.mocked(useScreenSize).mockReturnValue({
    screenSize: isPhoneWidth ? 'phone' : 'desktop',
    isPhoneWidth,
  } as ReturnType<typeof useScreenSize>);
}

function renderLayout() {
  return render(
    <MemoryRouter initialEntries={['/calendar/seattle']}>
      <Routes>
        <Route
          path="/calendar/seattle"
          element={
            <Layout>
              <div>cal</div>
            </Layout>
          }
        />
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  mockPhone(false);
});

describe('Layout', () => {
  it('shows nav and account menu when authenticated', () => {
    mockAuthed();
    renderLayout();
    expect(screen.getByRole('link', { name: 'Calendar' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /account menu/i })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /^sign in$/i })).not.toBeInTheDocument();
  });

  // Interests and Settings live in the account menu, so the nav is short
  // enough to keep Venues and About on phones too.
  const navLabels = () =>
    screen
      .getAllByRole('link')
      .map((l) => l.textContent)
      .filter(Boolean);

  it('renders the primary nav in order on desktop', () => {
    mockAuthed();
    renderLayout();
    expect(navLabels()).toEqual(['Calendar', 'Venues', 'About']);
  });

  it('renders the primary nav in order on phones', () => {
    mockAuthed();
    mockPhone(true);
    renderLayout();
    expect(navLabels()).toEqual(['Cal', 'Shows', 'Venues', 'About']);
  });
});
