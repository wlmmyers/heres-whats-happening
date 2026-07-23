import { useEffect, useRef, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { exchangeSpotifyCode } from '../api/spotify';
import * as s from './SpotifyCallbackPage.css';
import * as c from '../styles/common.css';

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
      // eslint-disable-next-line react-hooks/set-state-in-effect
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
        setTimeout(() => navigate('/calendar/seattle', { replace: true }), 1500);
      })
      .catch((err: Error) => {
        setStatus('error');
        setErrorMsg(err.message);
      });
  }, [params, navigate]);

  if (status === 'error') {
    return (
      <div className={c.pageHeader}>
        <h1 className={s.title}>Spotify connection failed</h1>
        <p className={s.message}>{errorMsg}</p>
        <button
          type="button"
          onClick={() => navigate('/settings', { replace: true })}
          className={s.backButton}
        >
          Back to settings
        </button>
      </div>
    );
  }

  if (status === 'success') {
    return (
      <div className={c.pageHeader}>
        <h1 className={s.title}>Spotify connected ✓</h1>
        <p className={s.message}>Redirecting you to your calendar…</p>
      </div>
    );
  }

  return (
    <div className={c.pageHeader}>
      <h1 className={s.title}>Connecting Spotify…</h1>
      <p className={s.message}>And creating your matches! Hang on a sec.</p>
    </div>
  );
}
