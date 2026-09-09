import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import AddManualEventDialog from './AddManualEventDialog';

vi.mock('../api/manualGoingEvents', () => ({
  listManualGoingEvents: vi.fn(),
  createManualGoingEvent: vi.fn(),
  updateManualGoingEvent: vi.fn(),
  deleteManualGoingEvent: vi.fn(),
}));

import { createManualGoingEvent } from '../api/manualGoingEvents';

function renderDialog(onClose = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const utils = render(
    <QueryClientProvider client={qc}>
      <AddManualEventDialog open onClose={onClose} />
    </QueryClientProvider>,
  );
  return { ...utils, onClose };
}

const dateField = () => screen.getByLabelText(/date/i);
const nameField = () => screen.getByLabelText(/event name/i);
const venueField = () => screen.getByLabelText(/venue/i);
const submit = () => screen.getByRole('button', { name: /add event/i });

async function fillValidForm(user: ReturnType<typeof userEvent.setup>) {
  await user.type(dateField(), '2026-03-10');
  await user.type(nameField(), 'Built to Spill');
  await user.type(venueField(), 'The Chapel');
}

beforeEach(() => {
  vi.resetAllMocks();
});

describe('AddManualEventDialog', () => {
  it('renders nothing when closed', () => {
    const qc = new QueryClient();
    render(
      <QueryClientProvider client={qc}>
        <AddManualEventDialog open={false} onClose={vi.fn()} />
      </QueryClientProvider>,
    );
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('refuses an empty form and names every missing field', async () => {
    const user = userEvent.setup();
    renderDialog();

    await user.click(submit());

    expect(await screen.findByText(/pick a date/i)).toBeInTheDocument();
    expect(screen.getByText(/enter an event name/i)).toBeInTheDocument();
    expect(screen.getByText(/enter a venue name/i)).toBeInTheDocument();
    expect(createManualGoingEvent).not.toHaveBeenCalled();
  });

  // A native date input silently drops an impossible day rather than storing
  // it, so what reaches validation is an empty field, not "2026-02-30".
  it('refuses a day the date picker itself rejects', async () => {
    const user = userEvent.setup();
    renderDialog();

    await user.type(dateField(), '2026-02-30');
    await user.type(nameField(), 'Built to Spill');
    await user.type(venueField(), 'The Chapel');
    await user.click(submit());

    expect(await screen.findByText(/pick a date/i)).toBeInTheDocument();
    expect(createManualGoingEvent).not.toHaveBeenCalled();
  });

  // The dialog's other date guard — a non-empty value that is not a real day —
  // cannot be reached from here: a `type="date"` input sanitises any such value
  // to "" however it is set, so no test at this level can put the field in that
  // state. It is covered where it lives, in parseLocalDate's own tests.

  it('refuses names that are only whitespace', async () => {
    const user = userEvent.setup();
    renderDialog();

    await user.type(dateField(), '2026-03-10');
    await user.type(nameField(), '   ');
    await user.type(venueField(), '   ');
    await user.click(submit());

    expect(await screen.findByText(/enter an event name/i)).toBeInTheDocument();
    expect(screen.getByText(/enter a venue name/i)).toBeInTheDocument();
    expect(createManualGoingEvent).not.toHaveBeenCalled();
  });

  it('posts the trimmed entry and closes on success', async () => {
    const user = userEvent.setup();
    vi.mocked(createManualGoingEvent).mockResolvedValue({
      id: 'm1',
      date: '2026-03-10',
      event_name: 'Built to Spill',
      venue_name: 'The Chapel',
      created_at: '2026-01-01T00:00:00Z',
    });
    const { onClose } = renderDialog();

    await user.type(dateField(), '2026-03-10');
    await user.type(nameField(), '  Built to Spill  ');
    await user.type(venueField(), '  The Chapel  ');
    await user.click(submit());

    await waitFor(() =>
      expect(createManualGoingEvent).toHaveBeenCalledWith({
        date: '2026-03-10',
        event_name: 'Built to Spill',
        venue_name: 'The Chapel',
      }),
    );
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it('stays open and reports the failure when the request fails', async () => {
    const user = userEvent.setup();
    vi.mocked(createManualGoingEvent).mockRejectedValue(new Error('nope'));
    const { onClose } = renderDialog();

    await fillValidForm(user);
    await user.click(submit());

    expect(await screen.findByText(/couldn.t add that show/i)).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
  });

  it('closes without posting when cancelled', async () => {
    const user = userEvent.setup();
    const { onClose } = renderDialog();

    await user.click(screen.getByRole('button', { name: /cancel/i }));

    expect(onClose).toHaveBeenCalled();
    expect(createManualGoingEvent).not.toHaveBeenCalled();
  });

  it('closes on Escape', async () => {
    const user = userEvent.setup();
    const { onClose } = renderDialog();

    await user.keyboard('{Escape}');

    expect(onClose).toHaveBeenCalled();
  });
});
