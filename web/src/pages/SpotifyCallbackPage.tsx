import { useEffect, useRef, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { exchangeSpotifyCode } from '../api/spotify';

type Status = 'exchanging' | 'success' | 'error';

export default function SpotifyCallbackPage() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const [status, setStatus] = useState<Status>('exchanging');
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  // React StrictMode double-invokes effects in dev; guard against running the
  // exchange twice (the second call would fail with state_mismatch because
  // the cookie has been cleared).
  const startedRef = useRef(false);

  useEffect(() => {
    if (startedRef.current) return;
    startedRef.current = true;

    const code = params.get('code');
    const state = params.get('state');
    const oauthError = params.get('error');

    if (oauthError) {
      setStatus('error');
      setErrorMsg(oauthError);
      return;
    }
    if (!code || !state) {
      setStatus('error');
      setErrorMsg('Missing code or state in callback URL');
      return;
    }

    exchangeSpotifyCode(code, state)
      .then(() => {
        setStatus('success');
        setTimeout(() => navigate('/calendar', { replace: true }), 1500);
      })
      .catch((err: Error) => {
        setStatus('error');
        setErrorMsg(err.message);
      });
  }, [params, navigate]);

  if (status === 'error') {
    return (
      <div className="text-center py-12">
        <h1 className="text-xl font-semibold">Spotify connection failed</h1>
        <p className="text-gray-600 mt-2">{errorMsg}</p>
        <button
          type="button"
          onClick={() => navigate('/settings', { replace: true })}
          className="mt-4 border rounded px-4 py-2 hover:bg-gray-50"
        >
          Back to settings
        </button>
      </div>
    );
  }

  if (status === 'success') {
    return (
      <div className="text-center py-12">
        <h1 className="text-xl font-semibold">Spotify connected ✓</h1>
        <p className="text-gray-600 mt-2">Redirecting you to your calendar…</p>
      </div>
    );
  }

  return (
    <div className="text-center py-12">
      <h1 className="text-xl font-semibold">Connecting Spotify…</h1>
      <p className="text-gray-600 mt-2">Hang on a sec.</p>
    </div>
  );
}
