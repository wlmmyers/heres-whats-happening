import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';

vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));

import { useAuth } from '../auth/useAuth';
import UserMenu from './UserMenu';

const logout = vi.fn();

function renderMenu() {
  return render(
    <MemoryRouter initialEntries={['/calendar/seattle']}>
      <UserMenu />
      <Routes>
        <Route path="/calendar/seattle" element={<div>calendar page</div>} />
        <Route path="/interests" element={<div>interests page</div>} />
        <Route path="/settings" element={<div>settings page</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

const openMenu = () => userEvent.click(screen.getByRole('button', { name: /account menu/i }));

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(useAuth).mockReturnValue({
    status: 'authenticated',
    user: { id: 'u', email: 'a@x.com', city_id: 'city-1', confirmed: true, show_setlists: false },
    login: vi.fn(),
    signup: vi.fn(),
    logout,
    refreshUser: vi.fn(),
    deleteAccount: vi.fn(),
  });
});

describe('UserMenu', () => {
  it('is closed until the avatar is clicked', async () => {
    renderMenu();
    expect(screen.queryByRole('link', { name: 'Interests' })).not.toBeInTheDocument();
    await openMenu();
    expect(screen.getByText('a@x.com')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Interests' })).toHaveAttribute('href', '/interests');
    expect(screen.getByRole('link', { name: 'Settings' })).toHaveAttribute('href', '/settings');
  });

  it.each([
    ['Interests', 'interests page'],
    ['Settings', 'settings page'],
  ])('navigates to %s and closes', async (label, page) => {
    renderMenu();
    await openMenu();
    await userEvent.click(screen.getByRole('link', { name: label }));
    expect(screen.getByText(page)).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: label })).not.toBeInTheDocument();
  });

  it('signs out', async () => {
    renderMenu();
    await openMenu();
    await userEvent.click(screen.getByRole('button', { name: 'Sign out' }));
    expect(logout).toHaveBeenCalled();
  });
});
