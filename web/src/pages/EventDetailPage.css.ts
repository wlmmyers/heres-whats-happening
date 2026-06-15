import { style } from '@vanilla-extract/css';
import { buttonPrimary } from '../styles/common.css';
import { color, radius, fontSize, fontWeight } from '../styles/theme';

export const backLink = style({
  display: 'block',
  ...fontSize.sm,
  color: color.blue600,
  fontWeight: fontWeight.bold,
  marginBottom: '0.625rem',
  ':hover': { textDecorationLine: 'underline' },
});

export const cover = style({
  width: '100%',
  maxHeight: '24rem',
  objectFit: 'cover',
  borderRadius: radius.sm,
  marginTop: '1rem',
});

export const header = style({ marginTop: '1rem' });

export const title = style({ ...fontSize['3xl'], fontWeight: fontWeight.semibold });

export const date = style({ color: color.gray700, marginTop: '0.25rem' });

export const venue = style({ color: color.gray600, marginTop: '0.25rem' });

export const score = style({ ...fontSize.sm, color: color.gray500, marginTop: '1rem' });

export const matched = style({
  backgroundColor: color.blue50,
  color: color.blue900,
  borderRadius: radius.sm,
  padding: '0.75rem',
  ...fontSize.sm,
  marginTop: '1rem',
});

export const description = style({
  color: color.gray800,
  whiteSpace: 'pre-wrap',
  marginTop: '1rem',
});

export const viewEvent = style([
  buttonPrimary,
  { fontWeight: fontWeight.bold, display: 'inline-block', marginTop: '1rem' },
]);

export const notFound = style({ color: color.gray700 });
