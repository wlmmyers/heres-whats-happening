import * as s from './ConfirmModals.css';
import * as c from '../styles/common.css';

/**
 * Shown after a successful confirmation when user is authenticated. When unauthenticated, user
 * will be shown the /login modal with a welcome message inside the modal
 */
export default function WelcomeModal({ onDismiss }: { onDismiss: () => void }) {
  return (
    <div className={s.backdrop}>
      <div role="dialog" aria-label="Welcome" aria-modal="true" className={s.confirmModal}>
        <h2 className={s.title}>You&rsquo;re all set</h2>
        <p className={s.body}>Your email is confirmed. Welcome!</p>
        <div className={s.actions}>
          <button type="button" onClick={onDismiss} className={c.buttonPrimary}>
            Got it
          </button>
        </div>
      </div>
    </div>
  );
}
