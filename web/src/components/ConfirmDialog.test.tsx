import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import ConfirmDialog from './ConfirmDialog';
import { DIALOG_ROOT_ID } from './Layout';

function addDialogRoot() {
  const root = document.createElement('div');
  root.id = DIALOG_ROOT_ID;
  document.body.appendChild(root);
  return root;
}

afterEach(() => {
  document.getElementById(DIALOG_ROOT_ID)?.remove();
});

describe('ConfirmDialog', () => {
  it('renders nothing when closed', () => {
    render(
      <ConfirmDialog
        open={false}
        title="Delete event?"
        message="Are you sure?"
        onConfirm={() => {}}
        onCancel={() => {}}
      />,
    );
    expect(screen.queryByText('Are you sure?')).toBeNull();
  });

  it('is named by its title and described by its message', () => {
    render(
      <ConfirmDialog
        open
        title="Delete event?"
        message="Are you sure?"
        onConfirm={() => {}}
        onCancel={() => {}}
      />,
    );
    expect(
      screen.getByRole('dialog', { name: 'Delete event?', description: 'Are you sure?' }),
    ).toBeInTheDocument();
  });

  it('fires onConfirm when Confirm is clicked', async () => {
    const onConfirm = vi.fn();
    render(
      <ConfirmDialog
        open
        title="Delete event?"
        message="Are you sure?"
        onConfirm={onConfirm}
        onCancel={() => {}}
      />,
    );
    await userEvent.click(screen.getByRole('button', { name: 'Confirm' }));
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it('fires onCancel when the backdrop is clicked', async () => {
    const onCancel = vi.fn();
    render(
      <ConfirmDialog
        open
        title="Delete event?"
        message="Are you sure?"
        onConfirm={() => {}}
        onCancel={onCancel}
      />,
    );
    await userEvent.click(screen.getByTestId('dialog-backdrop'));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('does not fire onCancel when the dialog body is clicked', async () => {
    const onCancel = vi.fn();
    render(
      <ConfirmDialog
        open
        title="Delete event?"
        message="Are you sure?"
        onConfirm={() => {}}
        onCancel={onCancel}
      />,
    );
    await userEvent.click(screen.getByText('Are you sure?'));
    expect(onCancel).not.toHaveBeenCalled();
  });

  // The backdrop is position: fixed, which resolves against the nearest
  // transformed ancestor rather than the viewport. Callers sit inside such
  // ancestors (the calendar sidebar under CalendarPage's translateY), so the
  // dialog has to leave the tree it was rendered from.
  it('renders into the app dialog root when one exists', () => {
    const root = addDialogRoot();
    render(
      <ConfirmDialog
        open
        title="Delete event?"
        message="Are you sure?"
        onConfirm={() => {}}
        onCancel={() => {}}
      />,
    );

    expect(root).toHaveTextContent('Are you sure?');
  });

  // A dialog that mounts closed with the page renders before Layout's root is
  // in the DOM, so resolving the host once on mount would pin it to the body
  // for good. It has to be resolved when the dialog actually opens.
  it('lands in the dialog root even when mounted closed before the root exists', () => {
    const { rerender } = render(
      <ConfirmDialog
        open={false}
        title="Delete event?"
        message="Are you sure?"
        onConfirm={() => {}}
        onCancel={() => {}}
      />,
    );
    const root = addDialogRoot();
    rerender(
      <ConfirmDialog
        open
        title="Delete event?"
        message="Are you sure?"
        onConfirm={() => {}}
        onCancel={() => {}}
      />,
    );

    expect(root).toHaveTextContent('Are you sure?');
  });

  it('falls back to the document body when there is no dialog root', () => {
    render(
      <ConfirmDialog
        open
        title="Delete event?"
        message="Are you sure?"
        onConfirm={() => {}}
        onCancel={() => {}}
      />,
    );

    expect(screen.getByText('Are you sure?')).toBeInTheDocument();
  });
});
