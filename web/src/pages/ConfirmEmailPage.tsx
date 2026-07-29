import { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useAuth } from '../auth/useAuth';
import { resendConfirmation } from '../api/auth';
import * as s from './ConfirmEmailPage.css';
import * as c from '../styles/common.css';

type ResendState = 'idle' | 'sending' | 'sent' | 'rate-limited' | 'failed';

export default function ConfirmEmailPage() {
  const { user, logout, refreshUser } = useAuth();
  const navigate = useNavigate();
  const [resend, setResend] = useState<ResendState>('idle');

  // "Signed up on the laptop, clicked the link on my phone" — on refocus,
  // re-mint the token and re-read /me so the stale tab moves along instead of
  // sitting on this page forever.
  const recheck = useCallback(async () => {
    try {
      const u = await refreshUser();
      if (u.confirmed) navigate('/calendar', { replace: true });
    } catch {
      // Offline, or the session lapsed; RequireAuth handles the latter.
    }
  }, [refreshUser, navigate]);

  useEffect(() => {
    window.addEventListener('focus', recheck);
    return () => window.removeEventListener('focus', recheck);
  }, [recheck]);

  async function onResend() {
    setResend('sending');
    try {
      await resendConfirmation();
      setResend('sent');
    } catch (err) {
      setResend((err as { code?: string }).code === 'rate_limited' ? 'rate-limited' : 'failed');
    }
  }

  return (
    <div className={s.card}>
      <h1 className={s.title}>Check your inbox</h1>
      <p className={s.body}>
        We sent a confirmation link to <span className={s.address}>{user?.email}</span>. Open it to
        finish setting up your account. The link expires in 24 hours.
      </p>

      <div className={s.actions}>
        <button
          type="button"
          onClick={onResend}
          disabled={resend === 'sending'}
          className={c.buttonPrimary}
        >
          {resend === 'sending' ? 'Sending…' : 'Resend the link'}
        </button>

        <div className={s.status} role="status">
          {resend === 'sent' && 'Sent — check your inbox again.'}
          {resend === 'rate-limited' && 'Too many requests. Try again in an hour.'}
          {resend === 'failed' && "We couldn't send that. Please try again."}
        </div>

        <button type="button" onClick={() => void logout()} className={s.linkButton}>
          Sign out
        </button>
      </div>
    </div>
  );
}
