import { style } from '@vanilla-extract/css';
import { color, radius, shadow, fontSize, fontWeight } from './theme';

// bg-white shadow rounded
export const surface = style({
  backgroundColor: color.white,
  boxShadow: shadow.base,
  borderRadius: radius.sm,
});

// bg-blue-600 hover:bg-blue-700 text-white rounded px-4 py-2 (+ disabled:opacity-60)
export const buttonPrimary = style({
  backgroundColor: color.blue600,
  color: color.white,
  borderRadius: radius.sm,
  paddingInline: '1rem',
  paddingBlock: '0.5rem',
  selectors: {
    '&:hover': { backgroundColor: color.blue700 },
    '&:disabled': { opacity: 0.6 },
  },
});

// border rounded px-4 py-2 hover:bg-gray-50 (+ disabled:opacity-60)
export const buttonSecondary = style({
  borderWidth: '1px',
  borderStyle: 'solid',
  borderRadius: radius.sm,
  paddingInline: '1rem',
  paddingBlock: '0.5rem',
  selectors: {
    '&:hover': { backgroundColor: color.gray50 },
    '&:disabled': { opacity: 0.6 },
  },
});

// w-full ... rounded py-2 disabled:opacity-50 (auth submit buttons)
export const buttonSubmit = style({
  width: '100%',
  backgroundColor: color.blue600,
  color: color.white,
  borderRadius: radius.sm,
  paddingBlock: '0.5rem',
  selectors: {
    '&:hover': { backgroundColor: color.blue700 },
    '&:disabled': { opacity: 0.5 },
  },
});

// mt-1 w-full border rounded px-2 py-1.5 (text fields)
export const textInput = style({
  marginTop: '0.25rem',
  width: '100%',
  borderWidth: '1px',
  borderStyle: 'solid',
  borderRadius: radius.sm,
  paddingInline: '0.5rem',
  paddingBlock: '0.375rem',
});

// text-2xl font-semibold
export const pageTitle = style({ ...fontSize['2xl'], fontWeight: fontWeight.semibold });

// text-lg font-medium
export const sectionTitle = style({ ...fontSize.lg, fontWeight: fontWeight.medium });

// text-red-600 text-sm
export const errorText = style({ ...fontSize.sm, color: color.red600 });
