import { useEffect, useState } from 'react';

const LOGO_URLS = [
  '/titleGraphic1.png',
  '/titleGraphic2.png',
  '/titleGraphic3.png',
  '/titleGraphic4.png',
  '/titleGraphic5.png',
];

export default function RotatingLogo({ className }: { className?: string }) {
  const [logoUrlIndex, setLogoUrlIndex] = useState(0);

  useEffect(() => {
    const interval = setInterval(() => {
      setLogoUrlIndex((prevIndex) => (prevIndex + 1) % LOGO_URLS.length);
    }, 600);
    return () => clearInterval(interval);
  }, []);

  return (
    <img
      src={LOGO_URLS[logoUrlIndex]}
      alt="Logo"
      style={{ width: '320px' }}
      className={className}
    />
  );
}
