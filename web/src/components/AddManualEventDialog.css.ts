import { style } from '@vanilla-extract/css';
import { sectionTitle } from '../styles/common.css';
import { color, fontSize } from '../styles/theme';

export const title = style([sectionTitle, { marginBottom: '1rem' }]);

export const description = style({
  ...fontSize.xs,
  color: color.gray700,
  marginBottom: '1rem',
});

export const form = style({
  display: 'flex',
  flexDirection: 'column',
  gap: '0.875rem',
});
