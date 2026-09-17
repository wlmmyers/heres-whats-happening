import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import DeleteAccountDialog from './DeleteAccountDialog';

vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));

import { useAuth } from '../auth/useAuth';

const deleteAccount = vi.fn();

function renderDialog(onClose = vi.fn()) {
  render(
    <MemoryRouter initialEntries={['/settings']}>
      <Routes>
        <Route path="/settings" element={<DeleteAccountDialog open onClose={onClose} />} />
        <Route path="/login" element={<p>Login screen</p>} />
      </Routes>
    </MemoryRouter>,
  );
  return { onClose };
}

const confirmField = () => screen.getByLabelText(/type delete account/i);
const deleteButton = () => screen.getByRole('button', { name: /delete my account/i });

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(useAuth).mockReturnValue({
    status: 'authenticated',
    user: { id: 'u1', email: 'a@x', city_id: 'city-1', confirmed: true, show_setlists: false },
    login: vi.fn(),
    signup: vi.fn(),
    logout: vi.fn(),
    refreshUser: vi.fn(),
    deleteAccount,
  });
});

describe('DeleteAccountDialog', () => {
  it('keeps the delete button disabled until "delete account" is typed', async () => {
    const user = userEvent.setup();
    renderDialog();

    expect(deleteButton()).toBeDisabled();

    await user.type(confirmField(), 'delete');
    expect(deleteButton()).toBeDisabled();

    await user.clear(confirmField());
    await user.type(confirmField(), 'Delete Account');
    expect(deleteButton()).toBeDisabled();

    await user.clear(confirmField());
    await user.type(confirmField(), '  delete account ');
    expect(deleteButton()).toBeEnabled();
  });

  it('does not delete when Enter is pressed before the phrase matches', async () => {
    const user = userEvent.setup();
    renderDialog();

    await user.type(confirmField(), 'delete acc{Enter}');

    expect(deleteAccount).not.toHaveBeenCalled();
  });

  it('deletes the account and sends the user to the login screen', async () => {
    deleteAccount.mockResolvedValueOnce(undefined);
    const user = userEvent.setup();
    renderDialog();

    await user.type(confirmField(), 'delete account');
    await user.click(deleteButton());

    expect(await screen.findByText('Login screen')).toBeInTheDocument();
    expect(deleteAccount).toHaveBeenCalledTimes(1);
  });

  it('stays open with an error when the delete fails', async () => {
    deleteAccount.mockRejectedValueOnce(new Error('500'));
    const user = userEvent.setup();
    const { onClose } = renderDialog();

    await user.type(confirmField(), 'delete account');
    await user.click(deleteButton());

    expect(await screen.findByRole('alert')).toHaveTextContent(/couldn.t delete your account/i);
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(screen.queryByText('Login screen')).toBeNull();
    expect(onClose).not.toHaveBeenCalled();
  });

  it('disables the delete button while the request is in flight', async () => {
    let finish: () => void = () => {};
    deleteAccount.mockReturnValueOnce(new Promise<void>((resolve) => (finish = resolve)));
    const user = userEvent.setup();
    renderDialog();

    await user.type(confirmField(), 'delete account');
    await user.click(deleteButton());

    await waitFor(() => expect(deleteButton()).toBeDisabled());
    await user.click(deleteButton());
    expect(deleteAccount).toHaveBeenCalledTimes(1);
    finish();
    expect(await screen.findByText('Login screen')).toBeInTheDocument();
  });

  it('closes without deleting when cancelled', async () => {
    const user = userEvent.setup();
    const { onClose } = renderDialog();

    await user.type(confirmField(), 'delete account');
    await user.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(onClose).toHaveBeenCalled();
    expect(deleteAccount).not.toHaveBeenCalled();
  });
});
