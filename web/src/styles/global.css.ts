import { globalStyle } from '@vanilla-extract/css';

// --- Preflight-equivalent reset (the part of Tailwind that is not a className) ---
// Mirrors Tailwind v4's Preflight so the app renders identically now that
// Tailwind is gone. Verified against the v4 build output.
globalStyle('*, ::before, ::after', {
  boxSizing: 'border-box',
  borderWidth: 0,
  borderStyle: 'solid',
  borderColor: 'currentColor',
  margin: 0,
  padding: 0,
});

globalStyle('html', { lineHeight: 1.5 });

globalStyle('body', {
  // App global: font carried over from the old styles.css.
  fontFamily: "'Raleway', sans-serif",
  fontOpticalSizing: 'auto',
});

globalStyle('h1, h2, h3, h4, h5, h6', {
  fontSize: 'inherit',
  fontWeight: 'inherit',
});

globalStyle('a', { color: 'inherit', textDecoration: 'inherit' });

globalStyle('button, input, optgroup, select, textarea', {
  font: 'inherit',
  color: 'inherit',
});

// App global: carried over from styles.css (cursor + weight) merged with the reset.
globalStyle('button', {
  cursor: 'pointer',
  fontWeight: 600,
  backgroundColor: 'transparent',
  backgroundImage: 'none',
});

globalStyle('img, svg, video, canvas', {
  display: 'block',
  maxWidth: '100%',
  height: 'auto',
});

globalStyle('ol, ul', { listStyle: 'none' });

globalStyle('hr', { height: 0, color: 'inherit', borderTopWidth: '1px' });

globalStyle('code, pre', { fontFamily: 'ui-monospace, monospace', fontSize: '1em' });
