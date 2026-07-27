import { Link } from 'react-router-dom';
import { useAuth } from '../auth/useAuth';
import * as s from './ConfirmModals.css';
import * as c from '../styles/common.css';

/**
 * Shown after a successful confirmation. Renders for anonymous visitors too:
 * the link is often opened on a phone that has no session, where the SPA
 * redirects to /login and this modal sits over the login screen.
 */
export default function WelcomeModal({ onDismiss }: { onDismiss: () => void }) {
  const { status } = useAuth();
  const authed = status === 'authenticated';

  return (
    <div className={s.backdrop}>
      <div role="dialog" aria-label="Welcome" aria-modal="true" className={s.card}>
        <h2 className={s.title}>You&rsquo;re all set</h2>
        <p className={s.body}>
          {authed
            ? 'Your email is confirmed. Welcome to Here’s What’s Happening.'
            : 'Your email is confirmed — now sign in to pick up where you left off.'}
        </p>
        <div className={s.actions}>
          {authed ? (
            <button type="button" onClick={onDismiss} className={c.buttonPrimary}>
              Got it
            </button>
          ) : (
            <Link to="/login" onClick={onDismiss} className={s.link}>
              Sign in
            </Link>
          )}
        </div>
      </div>
    </div>
  );
}
