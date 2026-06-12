import { style } from '@vanilla-extract/css';
import { buttonPrimary } from '../styles/common.css';
import { color, radius, fontSize, fontWeight } from '../styles/theme';

// block text-sm text-blue-600 hover:underline font-bold mb-2.5
export const backLink = style({
  display: 'block',
  ...fontSize.sm,
  color: color.blue600,
  fontWeight: fontWeight.bold,
  marginBottom: '0.625rem',
  ':hover': { textDecorationLine: 'underline' },
});

// w-full max-h-96 object-cover rounded  (+ space-y-4 marginTop)
export const cover = style({
  width: '100%',
  maxHeight: '24rem',
  objectFit: 'cover',
  borderRadius: radius.sm,
  marginTop: '1rem',
});

// header: space-y-1  (+ space-y-4 marginTop on the header itself)
export const header = style({ marginTop: '1rem' });

// text-3xl font-semibold
export const title = style({ ...fontSize['3xl'], fontWeight: fontWeight.semibold });

// text-gray-700  (+ space-y-1 marginTop)
export const date = style({ color: color.gray700, marginTop: '0.25rem' });

// text-gray-600  (+ space-y-1 marginTop)
export const venue = style({ color: color.gray600, marginTop: '0.25rem' });

// text-sm text-gray-500  (+ space-y-4 marginTop)
export const score = style({ ...fontSize.sm, color: color.gray500, marginTop: '1rem' });

// bg-blue-50 text-blue-900 rounded p-3 text-sm  (+ space-y-4 marginTop)
export const matched = style({
  backgroundColor: color.blue50,
  color: color.blue900,
  borderRadius: radius.sm,
  padding: '0.75rem',
  ...fontSize.sm,
  marginTop: '1rem',
});

// text-gray-800 whitespace-pre-wrap  (+ space-y-4 marginTop)
export const description = style({
  color: color.gray800,
  whiteSpace: 'pre-wrap',
  marginTop: '1rem',
});

// font-bold inline-block bg-blue-600 ... px-4 py-2  (View event; + space-y-4 marginTop)
export const viewEvent = style([
  buttonPrimary,
  { fontWeight: fontWeight.bold, display: 'inline-block', marginTop: '1rem' },
]);

// text-gray-700 (isError "Event not found")
export const notFound = style({ color: color.gray700 });
