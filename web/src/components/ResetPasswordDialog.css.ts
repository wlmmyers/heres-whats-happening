import { style } from '@vanilla-extract/css';

// One gap spaces every row: intro text, fields, error, button, footer link.
export const form = style({
  display: 'flex',
  flexDirection: 'column',
  gap: '1rem',
  marginTop: '1rem',
});
