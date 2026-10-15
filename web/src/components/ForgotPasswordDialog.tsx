import { useId, useState } from 'react';
import { Link } from 'react-router-dom';
import { requestPasswordReset } from '../api/auth';
import * as s from './ForgotPasswordDialog.css';
import * as d from './Dialog.css';
import * as c from '../styles/common.css';
import { useScreenSize } from '../hooks/useScreenSize';
import { LANDING_PAGE_KILL_ANIMATION } from '../constants/windowEvents';

/**
 * Asks for a reset link. The confirmation copy is the same whether or not an
 * account exists for the address — the API does not say, and neither do we.
 */
export default function ForgotPasswordDialog() {
  const headingId = useId();
  const { isPhoneWidth } = useScreenSize();
  const [email, setEmail] = useState('');
  const [sent, setSent] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function onSubmit(e: React.SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      await requestPasswordReset(email);
      setSent(true);
    } catch (err) {
      const code = (err as { code?: string }).code;
      if (code === 'rate_limited') {
        setError('Too many reset requests from your network. Please try again later.');
      } else {
        setError(err instanceof Error ? err.message : 'Could not send a reset link');
      }
    } finally {
      setSubmitting(false);
    }
  }

  const handleInputFocus = () => {
    if (isPhoneWidth) {
      // Kill landing page animation since it messes with the UI when phone keyboard is active
      window.dispatchEvent(new Event(LANDING_PAGE_KILL_ANIMATION));
    }
  };

  return (
    <div role="dialog" aria-labelledby={headingId} className={d.authDialogWrapper}>
      <div className={d.dialog}>
        <h1 id={headingId} className={d.dialogTitle}>
          Reset your password
        </h1>

        <form onSubmit={onSubmit} className={s.form}>
          {sent ? (
            <p className={d.body} role="status">
              If an account exists for <strong>{email}</strong>, we’ve sent it a link to reset the
              password. The link expires in 10 minutes.
            </p>
          ) : (
            <>
              <p className={d.body}>
                Enter your account’s email and we’ll send you a link to choose a new password.
              </p>

              <label className={c.field}>
                <span className={c.fieldLabel}>Email</span>
                <input
                  type="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  autoComplete="email"
                  required
                  className={c.textInput}
                  onFocus={handleInputFocus}
                />
              </label>

              {error && <div className={c.formError}>{error}</div>}

              <button type="submit" disabled={submitting} className={c.buttonSubmit}>
                {submitting ? 'Sending…' : 'Send reset link'}
              </button>
            </>
          )}

          <Link to="/login" className={c.link}>
            Back to sign in
          </Link>
        </form>
      </div>
    </div>
  );
}
