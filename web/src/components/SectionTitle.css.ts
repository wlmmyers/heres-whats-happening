import { style } from '@vanilla-extract/css';
import { color, fontSize, fontWeight } from '../styles/theme';

export const title = style({
  ...fontSize.xs,
  display: 'flex',
  alignItems: 'center',
  gap: '0.5rem',
  color: color.gray700,
  marginTop: '1.5rem',
  marginBottom: '1.5rem',
  fontWeight: fontWeight.medium,
  textTransform: 'uppercase',
  letterSpacing: '2px',
});

export const rule = style({
  flex: 1,
  borderColor: color.gray300,
});
