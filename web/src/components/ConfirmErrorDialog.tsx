import { useId, useState } from 'react';
import { Link } from 'react-router-dom';
import { useAuth } from '../auth/useAuth';
import { resendConfirmation } from '../api/auth';
import Dialog from './Dialog';
import * as dialogStyles from './Dialog.css';
import * as c from '../styles/common.css';

type SendState = 'idle' | 'sending' | 'sent' | 'rate-limited' | 'failed';

interface Props {
  open: boolean;
  onClose: () => void;
}

/**
 * Shown when a confirmation link was unknown or expired. An authenticated
 * visitor can mint a fresh one in place; an anonymous one has to sign in first,
 * because resend is an authenticated route.
 *
 * The send state lives in a child that only exists while the dialog is open, so
 * each opening starts from a fresh "Send a new link" button.
 */
export default function ConfirmErrorDialog({ open, onClose }: Props) {
  const bodyId = useId();
  return (
    <Dialog open={open} heading="That link didn’t work" onClose={onClose} aria-describedby={bodyId}>
      <ConfirmErrorBody bodyId={bodyId} onClose={onClose} />
    </Dialog>
  );
}

function ConfirmErrorBody({ bodyId, onClose }: { bodyId: string; onClose: () => void }) {
  const { status } = useAuth();
  const [send, setSend] = useState<SendState>('idle');

  async function onResend() {
    setSend('sending');
    try {
      await resendConfirmation();
      setSend('sent');
    } catch (err) {
      setSend((err as { code?: string }).code === 'rate_limited' ? 'rate-limited' : 'failed');
    }
  }

  return (
    <>
      <p id={bodyId} className={dialogStyles.body}>
        Confirmation links expire after 24 hours. We can send you a fresh one.
      </p>
      {status === 'authenticated' ? (
        <>
          <div className={c.dialogActions}>
            <button
              type="button"
              onClick={onResend}
              disabled={send === 'sending'}
              className={c.buttonPrimary}
            >
              {send === 'sending' ? 'Sending…' : 'Send a new link'}
            </button>
          </div>
          <div className={dialogStyles.status} role="status">
            {send === 'sent' && 'Sent - check your inbox.'}
            {send === 'rate-limited' && 'Too many requests. Try again in an hour.'}
            {send === 'failed' && "We couldn't send that. Please try again."}
          </div>
        </>
      ) : (
        <p className={dialogStyles.body}>
          <Link to="/login" onClick={onClose} className={c.link}>
            Sign in
          </Link>{' '}
          and we&rsquo;ll send a new link.
        </p>
      )}
    </>
  );
}
