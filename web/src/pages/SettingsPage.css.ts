import { style } from '@vanilla-extract/css';
import { surface, buttonPrimary, buttonSecondary, errorText } from '../styles/common.css';
import { color, radius, fontSize } from '../styles/theme';

export const section = style([surface, { padding: '1rem', marginTop: '2rem' }]);

export const desc = style({ ...fontSize.sm, color: color.gray700, marginTop: '0.75rem' });

// wrapper to give the TagInput (a child component) its top margin
export const item = style({ marginTop: '0.75rem' });

// --- Match sensitivity section ---
export const sliderRow = style({
  display: 'flex',
  alignItems: 'center',
  gap: '0.75rem',
  marginTop: '0.75rem',
});
export const slider = style({ flex: '1 1 0%' });
export const percent = style({
  width: '3rem',
  textAlign: 'right',
  ...fontSize.sm,
  fontVariantNumeric: 'tabular-nums',
});
export const saveButton = style([buttonPrimary, { marginTop: '0.75rem' }]);
export const error = style([errorText, { marginTop: '0.75rem' }]);

// --- Spotify section ---
export const row = style({
  display: 'flex',
  gap: '0.5rem',
  alignItems: 'center',
  marginTop: '0.75rem',
});
export const connectedText = style({ ...fontSize.sm, color: color.gray700 });
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
export const buttonRow = style({
  display: 'flex',
  flexWrap: 'wrap',
  gap: '0.5rem',
  alignItems: 'center',
  marginTop: '0.75rem',
});
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
export const resetButton = style([buttonSecondary, { marginTop: '0.75rem' }]);
