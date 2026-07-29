import { useState } from 'react';
import { Link } from 'react-router-dom';
import { useAuth } from '../auth/useAuth';
import { resendConfirmation } from '../api/auth';
import * as s from './ConfirmModals.css';
import * as c from '../styles/common.css';

type SendState = 'idle' | 'sending' | 'sent' | 'rate-limited' | 'failed';

/**
 * Shown when a confirmation link was unknown or expired. An authenticated
 * visitor can mint a fresh one in place; an anonymous one has to sign in first,
 * because resend is an authenticated route.
 */
export default function ConfirmErrorModal({ onDismiss }: { onDismiss: () => void }) {
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
    <div className={s.backdrop}>
      <div
        role="dialog"
        aria-label="Confirmation link problem"
        aria-modal="true"
        className={s.confirmModal}
      >
        <h2 className={s.title}>That link didn&rsquo;t work</h2>
        <p className={s.body}>
          Confirmation links expire after 24 hours. We can send you a fresh one.
        </p>

        <div className={s.actions}>
          {status === 'authenticated' ? (
            <>
              <button
                type="button"
                onClick={onResend}
                disabled={send === 'sending'}
                className={c.buttonPrimary}
              >
                {send === 'sending' ? 'Sending…' : 'Send a new link'}
              </button>
              <div className={s.status} role="status">
                {send === 'sent' && 'Sent — check your inbox.'}
                {send === 'rate-limited' && 'Too many requests. Try again in an hour.'}
                {send === 'failed' && "We couldn't send that. Please try again."}
              </div>
            </>
          ) : (
            <p className={s.body}>
              <Link to="/login" onClick={onDismiss} className={c.link}>
                Sign in
              </Link>{' '}
              and we&rsquo;ll send a new link.
            </p>
          )}
          <button type="button" onClick={onDismiss} className={c.linkButton}>
            Close
          </button>
        </div>
      </div>
    </div>
  );
}
