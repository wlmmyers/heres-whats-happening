import { style } from '@vanilla-extract/css';
import { color, radius, shadow, fontSize, fontWeight } from '../styles/theme';

export const root = style({ position: 'relative', marginLeft: 'auto' });

export const avatar = style({
  width: '2rem',
  height: '2rem',
  borderRadius: radius.full,
  backgroundColor: color.blue500,
  color: color.white,
  ...fontSize.sm,
  fontWeight: fontWeight.semibold,
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  cursor: 'pointer',
});

export const overlay = style({ position: 'fixed', inset: 0, zIndex: 10 });

export const dropdown = style({
  position: 'absolute',
  top: '100%',
  right: 0,
  marginTop: '0.25rem',
  zIndex: 20,
  minWidth: 'max-content',
  borderRadius: radius.sm,
  borderWidth: '1px',
  borderStyle: 'solid',
  borderColor: color.gray200,
  backgroundColor: color.white,
  paddingInline: '0.75rem',
  paddingBlock: '0.5rem',
  boxShadow: shadow.md,
});

export const email = style({ marginBottom: '0.5rem', ...fontSize.xs, color: color.gray500 });

export const divider = style({ marginBottom: '0.5rem', borderColor: color.gray100 });

export const signOut = style({
  cursor: 'pointer',
  borderWidth: 0,
  backgroundColor: 'transparent',
  padding: 0,
  ...fontSize.sm,
  color: color.gray700,
});
