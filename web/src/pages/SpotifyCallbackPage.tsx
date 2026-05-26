import { useEffect } from 'react';
import { useNavigate } from 'react-router-dom';

export default function SpotifyCallbackPage() {
  const navigate = useNavigate();
  useEffect(() => {
    const t = setTimeout(() => navigate('/calendar', { replace: true }), 1500);
    return () => clearTimeout(t);
  }, [navigate]);
  return (
    <div className="text-center py-12">
      <h1 className="text-xl font-semibold">Spotify connected ✓</h1>
      <p className="text-gray-600 mt-2">Redirecting you to your calendar…</p>
    </div>
  );
}
