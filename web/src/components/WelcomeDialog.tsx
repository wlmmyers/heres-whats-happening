import { useId } from 'react';
import Dialog from './Dialog';
import * as dialogStyles from './Dialog.css';
import * as c from '../styles/common.css';

interface Props {
  open: boolean;
  onClose: () => void;
}

/**
 * Shown after a successful confirmation when user is authenticated. When unauthenticated, user
 * will be shown the /login modal with a welcome message inside the modal
 */
export default function WelcomeDialog({ open, onClose }: Props) {
  const bodyId = useId();
  return (
    <Dialog open={open} heading="You're all set" onClose={onClose} aria-describedby={bodyId}>
      <p id={bodyId} className={dialogStyles.body}>
        Your email is confirmed. Welcome!
      </p>
      <div className={c.dialogActions}>
        <button type="button" onClick={onClose} className={c.buttonPrimary}>
          Got it
        </button>
      </div>
    </Dialog>
  );
}
