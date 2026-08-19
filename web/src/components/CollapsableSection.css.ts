import { style } from '@vanilla-extract/css';
import { color, fontSize } from '../styles/theme';
import { card } from '../styles/common.css';

export const collapsableSection = style([card, { margin: '1rem 0' }]);

// A <button>, so the reset's button rules apply: it needs to span the heading
// and keep the heading's own weight rather than the button default.
export const header = style({
  display: 'flex',
  alignItems: 'center',
  gap: '0.5rem',
  cursor: 'pointer',
  padding: '1rem',
  width: '100%',
  textAlign: 'left',
  fontWeight: 'inherit',
});

export const caret = style({
  display: 'block',
  width: '0.75rem',
  height: '0.75rem',
  color: color.gray500,
  marginLeft: '5px',
});

// Clips the body while its height animates between 0 and auto.
export const body = style({
  overflow: 'hidden',
});

export const bodyContent = style({
  color: color.gray800,
  whiteSpace: 'pre-wrap',
  fontSize: fontSize.sm.fontSize,
  padding: '0 1rem 1rem 1rem',
});
