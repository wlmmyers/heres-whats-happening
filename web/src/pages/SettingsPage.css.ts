import { style } from '@vanilla-extract/css';
import { surface, buttonPrimary, buttonSecondary, errorText } from '../styles/common.css';
import { color, radius, fontSize } from '../styles/theme';

// bg-white shadow rounded p-4 space-y-3  (+ space-y-8 marginTop)
export const section = style([surface, { padding: '1rem', marginTop: '2rem' }]);

// text-gray-700 text-sm  (+ space-y-3 marginTop) — the descriptive paragraphs
export const desc = style({ ...fontSize.sm, color: color.gray700, marginTop: '0.75rem' });

// wrapper to give the TagInput (a child component) its space-y-3 top margin
export const item = style({ marginTop: '0.75rem' });

// --- Match sensitivity section ---
// flex items-center gap-3  (+ space-y-3 marginTop)
export const sliderRow = style({
  display: 'flex',
  alignItems: 'center',
  gap: '0.75rem',
  marginTop: '0.75rem',
});
// flex-1 (range input)
export const slider = style({ flex: '1 1 0%' });
// w-12 text-right text-sm tabular-nums
export const percent = style({
  width: '3rem',
  textAlign: 'right',
  ...fontSize.sm,
  fontVariantNumeric: 'tabular-nums',
});
// Save button: buttonPrimary + space-y-3 marginTop (direct section child)
export const saveButton = style([buttonPrimary, { marginTop: '0.75rem' }]);
// saveError: errorText + space-y-3 marginTop
export const error = style([errorText, { marginTop: '0.75rem' }]);

// --- Spotify section ---
// flex gap-2 items-center  (+ space-y-3 marginTop)
export const row = style({
  display: 'flex',
  gap: '0.5rem',
  alignItems: 'center',
  marginTop: '0.75rem',
});
// text-sm text-gray-700 ("Connected.")
export const connectedText = style({ ...fontSize.sm, color: color.gray700 });
// bg-green-600 hover:bg-green-700 disabled:opacity-60 text-white rounded px-4 py-2
export const connectButton = style({
  backgroundColor: color.green600,
  color: color.white,
  borderRadius: radius.sm,
  paddingInline: '1rem',
  paddingBlock: '0.5rem',
  selectors: {
    '&:hover': { backgroundColor: color.green700 },
    '&:disabled': { opacity: 0.6 },
  },
});

// --- iCal section ---
// flex flex-wrap gap-2 items-center  (+ space-y-3 marginTop)
export const buttonRow = style({
  display: 'flex',
  flexWrap: 'wrap',
  gap: '0.5rem',
  alignItems: 'center',
  marginTop: '0.75rem',
});
// block bg-gray-100 rounded p-3 text-sm break-all  (+ space-y-3 marginTop)
export const codeBlock = style({
  display: 'block',
  backgroundColor: color.gray100,
  borderRadius: radius.sm,
  padding: '0.75rem',
  ...fontSize.sm,
  wordBreak: 'break-all',
  marginTop: '0.75rem',
});

// --- Hidden events section ---
// Reset button: buttonSecondary + space-y-3 marginTop (direct section child)
export const resetButton = style([buttonSecondary, { marginTop: '0.75rem' }]);
