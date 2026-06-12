import { style } from '@vanilla-extract/css';
import { surface, pageTitle } from '../styles/common.css';
import { color, radius, shadow, fontSize, fontWeight, transition } from '../styles/theme';

// flex flex-wrap items-baseline justify-between gap-3
export const header = style({
  display: 'flex',
  flexWrap: 'wrap',
  alignItems: 'baseline',
  justifyContent: 'space-between',
  gap: '0.75rem',
});

// flex items-center gap-2
export const controls = style({ display: 'flex', alignItems: 'center', gap: '0.5rem' });

// text-sm text-gray-500
export const controlLabel = style({ ...fontSize.sm, color: color.gray500 });

// inline-flex p-0.5
export const segment = style({ display: 'inline-flex', padding: '0.125rem' });

// rounded-md px-3 py-1 text-sm font-medium transition whitespace-nowrap (range button base)
export const rangeButton = style({
  borderRadius: radius.md,
  paddingInline: '0.75rem',
  paddingBlock: '0.25rem',
  ...fontSize.sm,
  fontWeight: fontWeight.medium,
  ...transition,
  whiteSpace: 'nowrap',
});

// active: bg-blue-600 text-white shadow-sm
export const rangeButtonActive = style({
  backgroundColor: color.blue600,
  color: color.white,
  boxShadow: shadow.sm,
});

// inactive: text-gray-600 hover:text-gray-900
export const rangeButtonInactive = style({
  color: color.gray600,
  ':hover': { color: color.gray900 },
});

// text-red-600  (+ space-y-4 marginTop)
export const errorBox = style({ color: color.red600, marginTop: '1rem' });

// bg-white shadow rounded p-8 text-center text-gray-600  (+ space-y-4 marginTop)
export const emptyState = style([
  surface,
  { padding: '2rem', textAlign: 'center', color: color.gray600, marginTop: '1rem' },
]);

// text-blue-600 underline (inline link in empty state)
export const inlineLink = style({ color: color.blue600, textDecorationLine: 'underline' });

// ul: space-y-3 list  (+ space-y-4 marginTop)
export const list = style({ marginTop: '1rem' });

// li spacing (space-y-3) — applied to every li except the first
export const listItem = style({ marginTop: '0.75rem' });

export { pageTitle };
