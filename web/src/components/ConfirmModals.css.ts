import { style } from '@vanilla-extract/css';
import { color, fontSize, fontWeight } from '../styles/theme';
import { cardNoShadow } from '../styles/common.css';

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

export const confirmModal = style([
  cardNoShadow,
  {
    padding: '2rem',
    maxWidth: '26rem',
    width: '100%',
    textAlign: 'center',
  },
]);

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
  color: color.gray600,
  marginTop: '0.75rem',
});

export const actions = style({
  display: 'flex',
  flexDirection: 'column',
  gap: '0.25rem',
});
