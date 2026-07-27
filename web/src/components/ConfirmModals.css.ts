import { style } from '@vanilla-extract/css';
import { color, fontSize, fontWeight, radius, shadow } from '../styles/theme';

export const backdrop = style({
  position: 'fixed',
  inset: 0,
  backgroundColor: color.blackA40,
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  zIndex: 100,
  padding: '1rem',
});

export const card = style({
  backgroundColor: color.white,
  boxShadow: shadow.lg,
  borderRadius: radius.md,
  padding: '2rem',
  maxWidth: '26rem',
  width: '100%',
  textAlign: 'center',
});

export const title = style({
  ...fontSize.xl,
  fontWeight: fontWeight.semibold,
  marginBottom: '0.75rem',
  color: color.gray900,
});

export const body = style({
  ...fontSize.base,
  color: color.gray600,
  marginBottom: '1.5rem',
});

export const status = style({
  ...fontSize.sm,
  minHeight: '1.25rem',
  color: color.gray600,
  marginTop: '0.75rem',
});

export const actions = style({
  display: 'flex',
  flexDirection: 'column',
  gap: '0.75rem',
  alignItems: 'center',
});

export const link = style({
  color: color.blue600,
  textDecoration: 'underline',
});
