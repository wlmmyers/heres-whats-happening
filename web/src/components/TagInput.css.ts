import { style } from '@vanilla-extract/css';
import { color, radius, fontSize } from '../styles/theme';

export const wrapper = style({
  borderWidth: '1px',
  borderStyle: 'solid',
  borderRadius: radius.sm,
  padding: '0.5rem',
  display: 'flex',
  flexWrap: 'wrap',
  gap: '0.5rem',
  alignItems: 'center',
});

export const tag = style({
  display: 'inline-flex',
  alignItems: 'center',
  backgroundColor: color.blue100,
  color: color.blue800,
  borderRadius: radius.full,
  paddingInline: '0.75rem',
  paddingBlock: '0.25rem',
  ...fontSize.sm,
});

export const removeButton = style({
  marginLeft: '0.5rem',
  color: color.blue700,
  ':hover': { color: color.red600 },
});

export const input = style({
  flex: '1 1 0%',
  minWidth: '120px',
  borderWidth: 0,
  outline: 'none',
  padding: '0.25rem',
  ...fontSize.sm,
});
