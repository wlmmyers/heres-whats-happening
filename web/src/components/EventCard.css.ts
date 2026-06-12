import { style } from '@vanilla-extract/css';
import { surface } from '../styles/common.css';
import { color, fontSize, fontWeight, shadow, transition } from '../styles/theme';

// bg-white shadow rounded p-4 hover:shadow-md transition  (= surface + ...)
export const card = style([
  surface,
  {
    padding: '1rem',
    ...transition,
    ':hover': { boxShadow: shadow.md },
  },
]);

// block
export const link = style({ display: 'block' });

// flex items-baseline justify-between gap-4
export const titleRow = style({
  display: 'flex',
  alignItems: 'baseline',
  justifyContent: 'space-between',
  gap: '1rem',
});

// text-lg font-semibold text-gray-900
export const title = style({
  ...fontSize.lg,
  fontWeight: fontWeight.semibold,
  color: color.gray900,
});

// text-sm text-gray-500 whitespace-nowrap
export const score = style({
  ...fontSize.sm,
  color: color.gray500,
  whiteSpace: 'nowrap',
});

// text-sm text-gray-700 mt-1
export const date = style({ ...fontSize.sm, color: color.gray700, marginTop: '0.25rem' });

// text-xs text-blue-700 mt-2
export const matched = style({ ...fontSize.xs, color: color.blue700, marginTop: '0.5rem' });

// mt-3 flex justify-end
export const notInterestedRow = style({
  marginTop: '0.75rem',
  display: 'flex',
  justifyContent: 'flex-end',
});

// text-sm text-gray-500 hover:text-red-600
export const notInterestedButton = style({
  ...fontSize.sm,
  color: color.gray500,
  ':hover': { color: color.red600 },
});
