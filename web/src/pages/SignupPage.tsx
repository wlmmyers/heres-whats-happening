import { useState, type FormEvent } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { useAuth } from '../auth/useAuth';

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
    <div className="min-h-screen bg-gray-50 flex items-center justify-center px-4">
      <form onSubmit={onSubmit} className="w-full max-w-sm bg-white shadow rounded p-6 space-y-4">
        <h1 className="text-xl font-semibold">Create your account</h1>

        <label className="block text-sm">
          <span className="text-gray-700">Email</span>
          <input
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            autoComplete="email"
            required
            className="mt-1 w-full border rounded px-2 py-1.5"
          />
        </label>

        <label className="block text-sm">
          <span className="text-gray-700">Password (min 8)</span>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="new-password"
            minLength={8}
            required
            className="mt-1 w-full border rounded px-2 py-1.5"
          />
        </label>

        {error && <div className="text-red-600 text-sm">{error}</div>}

        <button
          type="submit"
          disabled={submitting}
          className="w-full bg-blue-600 hover:bg-blue-700 text-white rounded py-2 disabled:opacity-50"
        >
          {submitting ? 'Creating…' : 'Create account'}
        </button>

        <p className="text-sm text-gray-600">
          Have an account?{' '}
          <Link to="/login" className="text-blue-600 hover:underline">
            Sign in
          </Link>
        </p>
      </form>
    </div>
  );
}
