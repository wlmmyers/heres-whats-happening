import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import Dialog from './Dialog';
import { DIALOG_ROOT_ID } from './Layout';

function renderDialog(onClose = vi.fn()) {
  render(
    <Dialog open heading="Pick a date" onClose={onClose}>
      <p>Dialog content</p>
    </Dialog>,
  );
  return { onClose };
}

afterEach(() => {
  document.getElementById(DIALOG_ROOT_ID)?.remove();
});

describe('Dialog', () => {
  it('renders nothing when closed', () => {
    render(
      <Dialog open={false} heading="Pick a date" onClose={vi.fn()}>
        <p>Dialog content</p>
      </Dialog>,
    );
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  // It stays mounted across close so AnimatePresence can play the exit, so the
  // exit has to actually finish and take the children with it -- otherwise a
  // closed dialog would linger, and state inside the children would survive to
  // the next open.
  it('removes itself and its children once closed', async () => {
    const { rerender } = render(
      <Dialog open heading="Pick a date" onClose={vi.fn()}>
        <p>Dialog content</p>
      </Dialog>,
    );
    rerender(
      <Dialog open={false} heading="Pick a date" onClose={vi.fn()}>
        <p>Dialog content</p>
      </Dialog>,
    );
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(screen.queryByText('Dialog content')).not.toBeInTheDocument();
  });

  it('is labelled by its heading and renders its children', () => {
    renderDialog();
    expect(screen.getByRole('dialog', { name: 'Pick a date' })).toHaveTextContent('Dialog content');
  });

  it('closes from the close button', async () => {
    const { onClose } = renderDialog();
    await userEvent.click(screen.getByRole('button', { name: 'Close' }));
    // Once, not twice: the click must not also bubble to the backdrop.
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('closes when the backdrop is clicked', async () => {
    const { onClose } = renderDialog();
    await userEvent.click(screen.getByTestId('dialog-backdrop'));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('does not close when the dialog body is clicked', async () => {
    const { onClose } = renderDialog();
    await userEvent.click(screen.getByText('Dialog content'));
    expect(onClose).not.toHaveBeenCalled();
  });

  it('closes on Escape', async () => {
    const { onClose } = renderDialog();
    await userEvent.keyboard('{Escape}');
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('renders into the app dialog root when one exists', () => {
    const root = document.createElement('div');
    root.id = DIALOG_ROOT_ID;
    document.body.appendChild(root);
    renderDialog();
    expect(root).toHaveTextContent('Dialog content');
  });
});
