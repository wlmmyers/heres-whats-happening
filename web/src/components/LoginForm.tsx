import { useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { useAuth } from '../auth/useAuth';
import RotatingLogo from './RotatingLogo';
import * as s from './LoginForm.css';
import * as c from '../styles/common.css';
import { useScreenSize } from '../hooks/useScreenSize';
import { LANDING_PAGE_KILL_ANIMATION } from '../constants/windowEvents';

export default function LoginForm() {
  const navigate = useNavigate();
  const { isPhoneWidth } = useScreenSize();
  const [params] = useSearchParams();
  const isWelcome = params.get('welcome') === 'true';
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
    <div className={s.loginCard}>
      <RotatingLogo />
      <div className={s.subtitle}>
        {isWelcome ? (
          <>
            Your email is confirmed.
            <br /> Sign in to add your interests and get started!
          </>
        ) : (
          <>
            An AI-backed event calendar based on you
            <aside className={s.aside}>Currently only supporting the Seattle area</aside>
          </>
        )}
      </div>
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
          <Link to="/signup" className={s.switchLink}>
            Sign up
          </Link>
        </p>
      </form>
    </div>
  );
}
