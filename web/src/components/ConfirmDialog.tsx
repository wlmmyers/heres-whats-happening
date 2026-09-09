import { createPortal } from 'react-dom';
import { DIALOG_ROOT_ID } from './Layout';
import * as s from './ConfirmDialog.css';
import * as c from '../styles/common.css';

interface Props {
  open: boolean;
  title?: string;
  message: string;
  confirmLabel?: string;
  cancelLabel?: string;
  onConfirm: () => void;
  onCancel: () => void;
}

export default function ConfirmDialog({
  open,
  title,
  message,
  confirmLabel = 'Confirm',
  cancelLabel = 'Cancel',
  onConfirm,
  onCancel,
}: Props) {
  if (!open) return null;
  // Resolved here rather than once on mount: a dialog that mounts closed with
  // its page renders before Layout has committed the root to the DOM, so a
  // mount-time lookup would miss it and pin the dialog to the body for good.
  // This component holds no state of its own, so re-resolving costs nothing.
  const host = document.getElementById(DIALOG_ROOT_ID) ?? document.body;
  return createPortal(
    <div data-testid="confirm-backdrop" className={c.backdrop} onClick={onCancel}>
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={title ? 'confirm-dialog-title' : undefined}
        aria-describedby="confirm-dialog-desc"
        className={c.dialog}
        onClick={(e) => e.stopPropagation()}
      >
        {title && (
          <h2 id="confirm-dialog-title" className={s.title}>
            {title}
          </h2>
        )}
        <p id="confirm-dialog-desc" className={s.message}>
          {message}
        </p>
        <div className={c.dialogActions}>
          <button type="button" onClick={onCancel} className={c.buttonSecondary}>
            {cancelLabel}
          </button>
          <button type="button" onClick={onConfirm} className={c.buttonPrimary}>
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>,
    host,
  );
}
