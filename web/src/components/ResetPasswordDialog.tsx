import { useEffect, useId, useState } from 'react';
import { Link, useLocation, useNavigate, useSearchParams } from 'react-router-dom';
import { resetPassword } from '../api/auth';
import { useAuth } from '../auth/useAuth';
import * as s from './ResetPasswordDialog.css';
import * as d from './Dialog.css';
import * as c from '../styles/common.css';
import { useScreenSize } from '../hooks/useScreenSize';
import { LANDING_PAGE_KILL_ANIMATION } from '../constants/windowEvents';

/**
 * The form the emailed reset link opens. Opening it spends nothing — only the
 * submit does — so a mail scanner prefetching the link cannot burn it.
 *
 * The token arrives as ?token= and is moved into history state straight away,
 * so it leaves the address bar (and with it any Referer, screenshot, or shared
 * URL) while still surviving a reload of the page.
 */
export default function ResetPasswordDialog() {
  const headingId = useId();
  const navigate = useNavigate();
  const location = useLocation();
  const [params] = useSearchParams();
  const { status, logout } = useAuth();
  const { isPhoneWidth } = useScreenSize();

  const urlToken = params.get('token');
  const stateToken = (location.state as { token?: string } | null)?.token;
  const token = urlToken || stateToken || '';

  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [linkInvalid, setLinkInvalid] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (urlToken) {
      navigate('/reset-password', { replace: true, state: { token: urlToken } });
    }
  }, [urlToken, navigate]);

  async function onSubmit(e: React.SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    if (password !== confirm) {
      setError('Passwords don’t match.');
      return;
    }
    setSubmitting(true);
    try {
      const email = await resetPassword(token, password);
      // The reset signed this account out everywhere server-side. Drop this
      // tab's session too, rather than leave it running on a revoked login.
      if (status === 'authenticated') {
        await logout().catch(() => {});
      }
      navigate(`/login?reset=true&email=${encodeURIComponent(email)}`, { replace: true });
    } catch (err) {
      const code = (err as { code?: string }).code;
      if (code === 'invalid_token') {
        setLinkInvalid(true);
      } else if (code === 'weak_password') {
        setError('Password must be at least 8 characters.');
      } else if (code === 'rate_limited') {
        setError('Too many attempts from your network. Please try again later.');
      } else {
        setError(err instanceof Error ? err.message : 'Could not reset your password');
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
        {!token || linkInvalid ? (
          <>
            <h1 id={headingId} className={c.sectionTitleLarge}>
              That link didn’t work
            </h1>
            <div className={s.form}>
              <p className={d.body}>
                This reset link is invalid or has expired. Links work once, for 10 minutes.
              </p>
              <Link to="/forgot-password" className={c.link}>
                Send a new link
              </Link>
            </div>
          </>
        ) : (
          <>
            <h1 id={headingId} className={d.dialogTitle}>
              Choose a new password
            </h1>
            <form onSubmit={onSubmit} className={s.form}>
              <label className={c.field}>
                <span className={c.fieldLabel}>New password (min 8)</span>
                <input
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  autoComplete="new-password"
                  minLength={8}
                  required
                  className={c.textInput}
                  onFocus={handleInputFocus}
                />
              </label>

              <label className={c.field}>
                <span className={c.fieldLabel}>Confirm new password</span>
                <input
                  type="password"
                  value={confirm}
                  onChange={(e) => setConfirm(e.target.value)}
                  autoComplete="new-password"
                  minLength={8}
                  required
                  className={c.textInput}
                  onFocus={handleInputFocus}
                />
              </label>

              {error && <div className={c.formError}>{error}</div>}

              <button type="submit" disabled={submitting} className={c.buttonSubmit}>
                {submitting ? 'Saving…' : 'Reset password'}
              </button>
            </form>
          </>
        )}
      </div>
    </div>
  );
}
