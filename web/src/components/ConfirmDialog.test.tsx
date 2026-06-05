import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import ConfirmDialog from './ConfirmDialog';

describe('ConfirmDialog', () => {
  it('renders nothing when closed', () => {
    render(
      <ConfirmDialog open={false} message="Are you sure?" onConfirm={() => {}} onCancel={() => {}} />,
    );
    expect(screen.queryByText('Are you sure?')).toBeNull();
  });

  it('fires onConfirm when Confirm is clicked', async () => {
    const onConfirm = vi.fn();
    render(
      <ConfirmDialog open message="Are you sure?" onConfirm={onConfirm} onCancel={() => {}} />,
    );
    await userEvent.click(screen.getByRole('button', { name: 'Confirm' }));
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it('fires onCancel when the backdrop is clicked', async () => {
    const onCancel = vi.fn();
    render(
      <ConfirmDialog open message="Are you sure?" onConfirm={() => {}} onCancel={onCancel} />,
    );
    await userEvent.click(screen.getByTestId('confirm-backdrop'));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('does not fire onCancel when the dialog body is clicked', async () => {
    const onCancel = vi.fn();
    render(
      <ConfirmDialog open message="Are you sure?" onConfirm={() => {}} onCancel={onCancel} />,
    );
    await userEvent.click(screen.getByText('Are you sure?'));
    expect(onCancel).not.toHaveBeenCalled();
  });
});
