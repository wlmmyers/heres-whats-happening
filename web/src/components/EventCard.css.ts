import { style } from '@vanilla-extract/css';
import { surface } from '../styles/common.css';
import { color, fontSize, fontWeight, shadow, transition } from '../styles/theme';

export const card = style([
  surface,
  {
    padding: '1rem',
    ...transition,
    ':hover': { boxShadow: shadow.md },
  },
]);

export const link = style({ display: 'block' });

export const titleRow = style({
  display: 'flex',
  alignItems: 'baseline',
  justifyContent: 'space-between',
  gap: '1rem',
});

export const title = style({
  ...fontSize.lg,
  fontWeight: fontWeight.semibold,
  color: color.gray900,
});

export const score = style({
  ...fontSize.sm,
  color: color.gray500,
  whiteSpace: 'nowrap',
});

export const date = style({ ...fontSize.sm, color: color.gray700, marginTop: '0.25rem' });

export const matched = style({ ...fontSize.xs, color: color.blue700, marginTop: '0.5rem' });

export const notInterestedRow = style({
  marginTop: '0.75rem',
  display: 'flex',
  justifyContent: 'flex-end',
});

export const notInterestedButton = style({
  ...fontSize.sm,
  color: color.gray500,
  ':hover': { color: color.red600 },
});
