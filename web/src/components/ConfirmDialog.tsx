import { useId } from 'react';
import Dialog from './Dialog';
import * as s from './ConfirmDialog.css';
import * as c from '../styles/common.css';

interface Props {
  open: boolean;
  title: string;
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
  const messageId = useId();
  return (
    <Dialog open={open} heading={title} onClose={onCancel} aria-describedby={messageId}>
      <p id={messageId} className={s.message}>
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
    </Dialog>
  );
}
