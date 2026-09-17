import { style } from '@vanilla-extract/css';
import { color, fontSize } from '../styles/theme';

export const message = style({
  ...fontSize.sm,
  color: color.gray700,
  marginBlock: '0.5rem 1rem',
});

export const form = style({
  display: 'flex',
  flexDirection: 'column',
  gap: '0.875rem',
});
