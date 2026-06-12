import { style } from '@vanilla-extract/css';
import { color } from '../styles/theme';

// flex items-center justify-center p-8 text-gray-500
export const root = style({
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  padding: '2rem',
  color: color.gray500,
});
