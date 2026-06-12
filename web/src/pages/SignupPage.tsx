import { useState, type FormEvent } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { useAuth } from '../auth/useAuth';
import * as s from './SignupPage.css';
import * as c from '../styles/common.css';

export default function SignupPage() {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const { signup } = useAuth();
  const navigate = useNavigate();

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      await signup(email, password);
      navigate('/onboarding', { replace: true });
    } catch (err) {
      const code = (err as { code?: string }).code;
      if (code === 'email_taken') {
        setError('An account with that email already exists.');
      } else if (code === 'weak_password') {
        setError('Password must be at least 8 characters.');
      } else {
        setError(err instanceof Error ? err.message : 'Signup failed');
      }
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className={s.page}>
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
          />
        </label>

        {error && <div className={s.error}>{error}</div>}

        <button
          type="submit"
          disabled={submitting}
          className={s.submit}
        >
          {submitting ? 'Creating…' : 'Create account'}
        </button>

        <p className={s.switchText}>
          Have an account?{' '}
          <Link to="/login" className={s.switchLink}>
            Sign in
          </Link>
        </p>
      </form>
    </div>
  );
}
