import { style } from '@vanilla-extract/css';

// NOTE: never write the token that names vanilla-extract's global stylesheet
// file inside a .css.ts file, even in a comment -- it breaks the
// vanilla-extract compile with a misleading "Styles were unable to be
// assigned to a file" error.

export const input = style({
  width: '100%',
  padding: '0.75rem',
  fontSize: '1rem',
  boxSizing: 'border-box',
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
});

export const optionActive = style({
  background: 'rgba(0, 0, 0, 0.06)',
});

export const meta = style({
  opacity: 0.7,
  fontSize: '0.85rem',
});

export const status = style({
  padding: '0.75rem',
  opacity: 0.7,
});
