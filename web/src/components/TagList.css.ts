import { style } from '@vanilla-extract/css';
import { color, radius, fontSize } from '../styles/theme';

export const wrapper = style({
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
