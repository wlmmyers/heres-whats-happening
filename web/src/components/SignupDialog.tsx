import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { useAuth } from '../auth/useAuth';
import * as s from './AuthDialog.css';
import * as c from '../styles/common.css';
import { useScreenSize } from '../hooks/useScreenSize';
import { LANDING_PAGE_KILL_ANIMATION } from '../constants/windowEvents';
import AuthDialog from './AuthDialog';

export default function SignupDialog() {
  const navigate = useNavigate();
  const { isPhoneWidth } = useScreenSize();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const { signup } = useAuth();

  async function onSubmit(e: React.SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      await signup(email, password);
      // A new account is unconfirmed, so it lands on /confirm-email rather
      // than in the product.
      navigate('/confirm-email');
    } catch (err) {
      const code = (err as { code?: string }).code;
      if (code === 'email_taken') {
        setError('An account with that email already exists.');
      } else if (code === 'weak_password') {
        setError('Password must be at least 8 characters.');
      } else if (code === 'rate_limited') {
        setError('Too many sign-ups from your network. Please try again later.');
      } else {
        setError(err instanceof Error ? err.message : 'Signup failed');
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
      ariaLabel="Sign up"
      formContent={
        <form onSubmit={onSubmit} className={s.form}>
          <h1 className={s.title}>Create your account</h1>

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
            <span className={s.fieldLabel}>Password (min 8)</span>
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

          {error && <div className={s.error}>{error}</div>}

          <button type="submit" disabled={submitting} className={s.submit}>
            {submitting ? 'Creating…' : 'Create account'}
          </button>

          <p className={s.switchText}>
            Have an account?{' '}
            <Link to="/login" className={c.link}>
              Sign in
            </Link>
          </p>
        </form>
      }
    />
  );
}
