import { style } from '@vanilla-extract/css';
import { color, fontSize, fontWeight, radius, shadow } from '../styles/theme';

export const card = style({
  maxWidth: '28rem',
  margin: '4rem auto',
  padding: '2rem',
  textAlign: 'center',
  backgroundColor: color.white,
  boxShadow: shadow.base,
  borderRadius: radius.sm,
});

export const title = style({
  ...fontSize['2xl'],
  fontWeight: fontWeight.semibold,
  marginBottom: '0.75rem',
  color: color.gray900,
});

export const body = style({
  ...fontSize.base,
  color: color.gray600,
  marginBottom: '1.5rem',
});

export const address = style({
  fontWeight: fontWeight.semibold,
  color: color.gray900,
  wordBreak: 'break-all',
});

export const actions = style({
  display: 'flex',
  flexDirection: 'column',
  gap: '0.75rem',
  alignItems: 'center',
});

export const status = style({
  ...fontSize.sm,
  minHeight: '1.25rem',
  color: color.gray600,
});

export const linkButton = style({
  ...fontSize.sm,
  background: 'none',
  border: 'none',
  padding: 0,
  color: color.gray500,
  textDecoration: 'underline',
  cursor: 'pointer',
  selectors: {
    '&:hover': { color: color.gray700 },
  },
});
