import { style } from '@vanilla-extract/css';
import { surface, errorText, buttonSubmit } from '../styles/common.css';
import { color, fontSize, fontWeight } from '../styles/theme';

// min-h-screen bg-gray-50 flex flex-col items-center justify-center px-4  (md: row + gap-8)
export const page = style({
  minHeight: '100vh',
  backgroundColor: color.gray50,
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'center',
  justifyContent: 'center',
  paddingInline: '1rem',
  '@media': {
    'screen and (min-width: 768px)': { flexDirection: 'row', gap: '2rem' },
  },
});

// mb-8 (logo image)
export const logo = style({ marginBottom: '2rem' });

// w-full max-w-sm p-6 space-y-4  (+ surface card, matching SignupPage)
export const form = style([
  surface,
  { width: '100%', maxWidth: '24rem', padding: '1.5rem' },
]);

// text-xl font-semibold
export const title = style({ ...fontSize.xl, fontWeight: fontWeight.semibold });

// block text-sm  (+ space-y-4 marginTop)
export const field = style({ display: 'block', ...fontSize.sm, marginTop: '1rem' });

// text-gray-700 (label text)
export const fieldLabel = style({ color: color.gray700 });

// text-red-600 text-sm  (+ space-y-4 marginTop)
export const error = style([errorText, { marginTop: '1rem' }]);

// w-full ... py-2 disabled:opacity-50  (+ space-y-4 marginTop)
export const submit = style([buttonSubmit, { marginTop: '1rem' }]);

// text-sm text-gray-600  (+ space-y-4 marginTop)
export const switchText = style({ ...fontSize.sm, color: color.gray600, marginTop: '1rem' });

// text-blue-600 hover:underline
export const switchLink = style({
  color: color.blue600,
  ':hover': { textDecorationLine: 'underline' },
});
