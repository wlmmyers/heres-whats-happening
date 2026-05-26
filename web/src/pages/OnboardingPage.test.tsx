import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import OnboardingPage from './OnboardingPage';

vi.mock('../api/interests', () => ({
  listInterests: vi.fn(),
  createInterest: vi.fn(),
  deleteInterest: vi.fn(),
}));

import * as interestsApi from '../api/interests';

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/onboarding']}>
        <Routes>
          <Route path="/onboarding" element={<OnboardingPage />} />
          <Route path="/calendar" element={<div>calendar-route</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  (interestsApi.listInterests as ReturnType<typeof vi.fn>).mockResolvedValue([]);
});

describe('OnboardingPage', () => {
  it('lists existing interests and lets the user add one', async () => {
    (interestsApi.listInterests as ReturnType<typeof vi.fn>)
      .mockResolvedValueOnce([{ id: 'i1', value: 'jazz', normalized_value: 'jazz', weight: 1, created_at: '' }])
      .mockResolvedValueOnce([
        { id: 'i1', value: 'jazz', normalized_value: 'jazz', weight: 1, created_at: '' },
        { id: 'i2', value: 'theater', normalized_value: 'theater', weight: 1, created_at: '' },
      ]);
    (interestsApi.createInterest as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      id: 'i2', value: 'theater', normalized_value: 'theater', weight: 1, created_at: '',
    });
    renderPage();
    await waitFor(() => expect(screen.getByText('jazz')).toBeInTheDocument());

    await userEvent.type(screen.getByPlaceholderText(/add an interest/i), 'theater{enter}');
    await waitFor(() => expect(interestsApi.createInterest).toHaveBeenCalledWith('theater'));
    await waitFor(() => expect(screen.getByText('theater')).toBeInTheDocument());
  });

  it('Continue navigates to calendar', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByRole('button', { name: /continue/i })).toBeEnabled());
    await userEvent.click(screen.getByRole('button', { name: /continue/i }));
    expect(screen.getByText(/calendar-route/)).toBeInTheDocument();
  });
});
