import { style } from '@vanilla-extract/css';
import { color } from '../styles/theme';

export const root = style({
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  padding: '2rem',
  color: color.gray500,
});
