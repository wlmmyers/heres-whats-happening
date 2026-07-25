import { style } from '@vanilla-extract/css';
import { clickableCard } from '../styles/common.css';
import { color, fontSize, fontWeight, radius, transition } from '../styles/theme';

export const eventCard = style([
  clickableCard,
  {
    padding: '1rem',
    minHeight: '150px',
    ...transition,
  },
]);

export const link = style({
  display: 'flex',
  alignItems: 'flex-start',
  gap: '0.75rem',
});

export const thumbnail = style({
  flexShrink: 0,
  width: '5rem',
  height: '5rem',
  objectFit: 'cover',
  borderRadius: radius.sm,
});

export const body = style({ minWidth: 0, flexGrow: 1 });

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

export const date = style({
  ...fontSize.sm,
  color: color.gray700,
  marginTop: '0.25rem',
});

export const matched = style({
  ...fontSize.xs,
  color: color.blue700,
  marginTop: '0.5rem',
});

export const notInterestedRow = style({
  marginTop: '0.75rem',
  display: 'flex',
  justifyContent: 'flex-end',
});

export const notInterestedButton = style({
  ...fontSize.xs,
  fontWeight: fontWeight.medium,
  border: '1px solid',
  borderColor: color.gray200,
  borderRadius: radius.sm,
  paddingInline: '0.5rem',
  paddingBlock: '0.25rem',
  backgroundColor: color.white,
  ...transition,
  color: color.gray500,
  ':hover': { color: color.red600, borderColor: color.red600 },
});
