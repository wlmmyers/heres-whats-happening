import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../api/auth', () => ({ requestPasswordReset: vi.fn() }));

import { requestPasswordReset } from '../api/auth';
import ForgotPasswordDialog from './ForgotPasswordDialog';

beforeEach(() => {
  vi.resetAllMocks();
});

function renderDialog() {
  render(
    <MemoryRouter initialEntries={['/forgot-password']}>
      <ForgotPasswordDialog />
    </MemoryRouter>,
  );
}

describe('ForgotPasswordDialog', () => {
  it('renders the email form without a forgot-password link of its own', () => {
    renderDialog();
    expect(screen.getByRole('dialog', { name: /reset your password/i })).toBeInTheDocument();
    expect(screen.getByLabelText(/email/i)).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /forgot password/i })).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: /sign in/i })).toHaveAttribute('href', '/login');
  });

  it('requests a link and shows the same confirmation for any address', async () => {
    vi.mocked(requestPasswordReset).mockResolvedValueOnce(undefined);
    renderDialog();
    await userEvent.type(screen.getByLabelText(/email/i), 'a@x.com');
    await userEvent.click(screen.getByRole('button', { name: /send reset link/i }));

    expect(requestPasswordReset).toHaveBeenCalledWith('a@x.com');
    const status = await screen.findByRole('status');
    expect(status).toHaveTextContent(/if an account exists for a@x\.com/i);
    expect(status).toHaveTextContent(/10 minutes/i);
    expect(screen.queryByRole('button', { name: /send reset link/i })).not.toBeInTheDocument();
  });

  it('shows a rate-limit message', async () => {
    vi.mocked(requestPasswordReset).mockRejectedValueOnce(
      Object.assign(new Error('slow down'), { code: 'rate_limited' }),
    );
    renderDialog();
    await userEvent.type(screen.getByLabelText(/email/i), 'a@x.com');
    await userEvent.click(screen.getByRole('button', { name: /send reset link/i }));
    expect(await screen.findByText(/too many reset requests/i)).toBeInTheDocument();
  });
});
