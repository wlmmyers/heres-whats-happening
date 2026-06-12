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
  return (
    <div
      data-testid="confirm-backdrop"
      className={s.backdrop}
      onClick={onCancel}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={title ? 'confirm-dialog-title' : undefined}
        aria-describedby="confirm-dialog-desc"
        className={s.dialog}
        onClick={(e) => e.stopPropagation()}
      >
        {title && <h2 id="confirm-dialog-title" className={s.title}>{title}</h2>}
        <p id="confirm-dialog-desc" className={s.message}>{message}</p>
        <div className={s.actions}>
          <button
            type="button"
            onClick={onCancel}
            className={c.buttonSecondary}
          >
            {cancelLabel}
          </button>
          <button
            type="button"
            onClick={onConfirm}
            className={c.buttonPrimary}
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
