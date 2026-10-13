import { style } from '@vanilla-extract/css';
import { color } from '../styles/theme';

export const logoWrap = style({
  maxWidth: '320px',
  margin: '1rem auto 2rem auto',
  padding: '0.5rem',
  backgroundColor: 'white',
  boxShadow: `0 0 0 9px ${color.blue100}`,
  border: `3px solid ${color.blue300}`,
});
