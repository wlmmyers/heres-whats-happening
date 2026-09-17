import { style } from '@vanilla-extract/css';
import { color, fontSize } from '../styles/theme';
import { phone } from '../styles/breakpoints.css';

// NOTE: never write the token that names vanilla-extract's global stylesheet
// file inside a .css.ts file, even in a comment -- it breaks the
// vanilla-extract compile with a misleading "Styles were unable to be
// assigned to a file" error.

export const searchDialog = style({
  width: '40rem',
  maxWidth: '90%',
});

export const searchField = style({
  marginTop: '0.875rem',
  marginBottom: '0.25rem',
});

export const list = style({
  listStyle: 'none',
  margin: '0.5rem 0 0',
  padding: 0,
  maxHeight: '20rem',
  overflowY: 'auto',
});

export const option = style({
  padding: '0.5rem 0.75rem',
  cursor: 'pointer',
  '@media': {
    [phone]: { padding: '0.5rem 0' },
  },
});

export const optionActive = style({
  background: 'rgba(0, 0, 0, 0.06)',
});

export const meta = style({
  color: color.gray400,
  fontSize: '0.85rem',
});

export const status = style({
  paddingBlock: '0.75rem',
  ...fontSize.sm,
  color: color.gray400,
});
