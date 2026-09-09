import { style } from '@vanilla-extract/css';
import { color } from '../styles/theme';

export const caret = style({
  display: 'block',
  flexShrink: 0,
  width: '0.75rem',
  height: '0.75rem',
  color: color.gray500,
});
