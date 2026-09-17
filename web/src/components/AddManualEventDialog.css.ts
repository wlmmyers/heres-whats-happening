import { style } from '@vanilla-extract/css';
import { color, fontSize } from '../styles/theme';

export const description = style({
  ...fontSize.xs,
  color: color.gray700,
  marginBlock: '1rem',
});

export const form = style({
  display: 'flex',
  flexDirection: 'column',
  gap: '0.875rem',
});
