import { style } from '@vanilla-extract/css';
import { buttonPrimary } from '../styles/common.css';
import { color, radius, fontSize, fontWeight, textStroke } from '../styles/theme';
import { phone } from '../styles/breakpoints.css';

export const backLink = style({
  display: 'block',
  ...fontSize.sm,
  color: color.blue600,
  fontWeight: fontWeight.medium,
  marginBottom: '0.625rem',
  height: '30px',
  ...textStroke('4px'),
  ':hover': { textDecorationLine: 'underline' },
});

export const thumbnail = style({
  width: '9rem',
  height: '9rem',
  objectFit: 'cover',
  borderRadius: radius.sm,
  '@media': {
    [phone]: { width: '100%', height: 'auto' },
  },
});

export const detail = style({
  display: 'flex',
  justifyContent: 'space-between',
  minHeight: '144px',
  '@media': {
    [phone]: { flexDirection: 'column' },
  },
});

export const detailText = style({
  minWidth: 0,
  flexGrow: 1,
  padding: '0.5rem 1rem',
  '@media': {
    [phone]: { padding: '1rem' },
  },
});

export const title = style({
  ...fontSize['2xl'],
  fontWeight: fontWeight.semibold,
});

export const date = style({ color: color.gray700, marginTop: '0.25rem' });

export const venue = style({ color: color.gray600, marginTop: '0.25rem' });

export const score = style({
  ...fontSize.sm,
  color: color.gray500,
  marginTop: '0.5rem',
});

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
  fontSize: fontSize.sm.fontSize,
});

export const viewEvent = style([buttonPrimary, { display: 'inline-block', marginTop: '1rem' }]);

export const notFound = style({ color: color.gray700 });
