import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

// The page renders Layout itself, and Layout reads the auth context.
vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));

import { useAuth } from '../auth/useAuth';
import AboutPage from './AboutPage';

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/about']}>
      <AboutPage />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  // /about sits behind RequireAuth, so the page is only ever reached authenticated.
  vi.mocked(useAuth).mockReturnValue({
    status: 'authenticated',
    user: { id: 'u1', email: 'a@x', city_id: 'city-1', confirmed: true, show_setlists: false },
    login: vi.fn(),
    signup: vi.fn(),
    logout: vi.fn(),
    refreshUser: vi.fn(),
    deleteAccount: vi.fn(),
  });
});

describe('AboutPage', () => {
  it('leads with the rotating logo', () => {
    renderPage();
    expect(screen.getByRole('img', { name: /logo/i })).toBeInTheDocument();
  });

  it('lists the user-facing features', () => {
    renderPage();
    expect(screen.getByText('Rich ingestion of events')).toBeInTheDocument();
    expect(screen.getByText('Interests from your listening history and more')).toBeInTheDocument();
  });

  it('renders every feature and roadmap item', () => {
    renderPage();
    expect(screen.getAllByRole('listitem')).toHaveLength(9);
  });
});
