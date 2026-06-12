import { useState, type FormEvent } from 'react';
import { Link, useNavigate, useLocation } from 'react-router-dom';
import { useAuth } from '../auth/useAuth';
import * as s from './LoginPage.css';
import * as c from '../styles/common.css';

interface LocationState {
  from?: { pathname?: string };
}

export default function LoginPage() {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const { login } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const dest = (location.state as LocationState | null)?.from?.pathname ?? '/calendar';

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      await login(email, password);
      navigate(dest, { replace: true });
    } catch (err) {
      const code = (err as { code?: string }).code;
      if (code === 'invalid_credentials') {
        setError('Email or password is wrong');
      } else {
        setError(err instanceof Error ? err.message : 'Login failed');
      }
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className={s.page}>
      <img src="/titleLogo.png" alt="Logo" style={{ width: '300px' }} className={s.logo} />
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
          />
        </label>

        {error && <div className={s.error}>{error}</div>}

        <button
          type="submit"
          disabled={submitting}
          className={s.submit}
        >
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
