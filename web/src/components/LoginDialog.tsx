import { useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { useAuth } from '../auth/useAuth';
import * as s from './AuthDialog.css';
import * as c from '../styles/common.css';
import { useScreenSize } from '../hooks/useScreenSize';
import { LANDING_PAGE_KILL_ANIMATION } from '../constants/windowEvents';
import AuthDialog from './AuthDialog';

export default function LoginDialog() {
  const navigate = useNavigate();
  const { isPhoneWidth } = useScreenSize();
  const [params] = useSearchParams();
  const prefilledEmail = params.get('email');
  const [email, setEmail] = useState(prefilledEmail || '');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const { login } = useAuth();

  async function onSubmit(e: React.SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      await login(email, password);
      navigate('/calendar/seattle');
    } catch (err) {
      const code = (err as { code?: string }).code;
      if (code === 'invalid_credentials') {
        setError('Email or password is wrong');
      } else if (code === 'rate_limited') {
        setError('Too many login attempts. Please wait a moment and try again.');
      } else {
        setError(err instanceof Error ? err.message : 'Login failed');
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
    <AuthDialog
      ariaLabel="Sign in"
      formContent={
        <form onSubmit={onSubmit} className={s.form}>
          <h1 className={s.title}>Sign in</h1>

          <label className={s.field}>
            <span className={s.fieldLabel}>Email</span>
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

          <label className={s.field}>
            <span className={s.fieldLabel}>Password</span>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
              required
              className={c.textInput}
              onFocus={handleInputFocus}
            />
          </label>

          {error && <div className={s.error}>{error}</div>}

          <button type="submit" disabled={submitting} className={s.submit}>
            {submitting ? 'Signing in…' : 'Sign in'}
          </button>

          <p className={s.switchText}>
            No account?{' '}
            <Link to="/signup" className={c.link}>
              Sign up
            </Link>
          </p>
        </form>
      }
    />
  );
}
