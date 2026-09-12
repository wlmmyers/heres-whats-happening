import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../api/search', () => ({ searchEvents: vi.fn() }));

const navigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom');
  return { ...actual, useNavigate: () => navigate };
});

import { searchEvents } from '../api/search';
import SearchDialog from './SearchDialog';

function renderDialog(onClose = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const utils = render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <SearchDialog open onClose={onClose} cityId="city-1" />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { ...utils, onClose };
}

const field = () => screen.getByRole('combobox');

const oneResult = {
  results: [
    {
      id: 'e1',
      title: 'Midnight Orchard',
      starts_at: '2026-10-02T03:00:00Z',
      venue: { name: 'The Bowl' },
    },
  ],
};

beforeEach(() => {
  vi.resetAllMocks();
  navigate.mockReset();
});

describe('SearchDialog', () => {
  it('renders nothing when closed', () => {
    const qc = new QueryClient();
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <SearchDialog open={false} onClose={vi.fn()} cityId="city-1" />
        </MemoryRouter>
      </QueryClientProvider>,
    );
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('prompts rather than querying below the minimum length', async () => {
    const user = userEvent.setup();
    renderDialog();
    await user.type(field(), 'mi');
    expect(searchEvents).not.toHaveBeenCalled();
    expect(screen.getByText(/keep typing/i)).toBeInTheDocument();
  });

  it('renders results as options', async () => {
    vi.mocked(searchEvents).mockResolvedValue(oneResult);
    const user = userEvent.setup();
    renderDialog();
    await user.type(field(), 'midnight');
    await waitFor(() => expect(screen.getByRole('option')).toHaveTextContent('Midnight Orchard'));
    expect(screen.getByRole('option')).toHaveTextContent('The Bowl');
  });

  it('shows an empty state when nothing matches', async () => {
    vi.mocked(searchEvents).mockResolvedValue({ results: [] });
    const user = userEvent.setup();
    renderDialog();
    await user.type(field(), 'zzzqqq');
    await waitFor(() => expect(screen.getByText(/no events match/i)).toBeInTheDocument());
  });

  it('navigates to the event on Enter and closes', async () => {
    vi.mocked(searchEvents).mockResolvedValue(oneResult);
    const user = userEvent.setup();
    const { onClose } = renderDialog();
    await user.type(field(), 'midnight');
    await waitFor(() => expect(screen.getByRole('option')).toBeInTheDocument());
    await user.keyboard('{ArrowDown}{Enter}');
    expect(navigate).toHaveBeenCalledWith('/events/e1');
    expect(onClose).toHaveBeenCalled();
  });

  it('closes on Escape', async () => {
    const user = userEvent.setup();
    const { onClose } = renderDialog();
    await user.keyboard('{Escape}');
    expect(onClose).toHaveBeenCalled();
  });
});
