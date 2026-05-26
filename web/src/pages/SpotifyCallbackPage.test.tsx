import { describe, it, expect } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import { vi } from 'vitest';
import SpotifyCallbackPage from './SpotifyCallbackPage';

describe('SpotifyCallbackPage', () => {
  it('renders the connected message and redirects after delay', async () => {
    vi.useFakeTimers();
    try {
      render(
        <MemoryRouter initialEntries={['/integrations/spotify/callback']}>
          <Routes>
            <Route path="/integrations/spotify/callback" element={<SpotifyCallbackPage />} />
            <Route path="/calendar" element={<div>calendar-route</div>} />
          </Routes>
        </MemoryRouter>,
      );
      expect(screen.getByText(/spotify connected/i)).toBeInTheDocument();
      expect(screen.queryByText(/calendar-route/)).not.toBeInTheDocument();

      // Advance past the 1.5s redirect timer.
      await act(async () => {
        vi.advanceTimersByTime(1600);
      });

      expect(screen.getByText(/calendar-route/)).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });
});
