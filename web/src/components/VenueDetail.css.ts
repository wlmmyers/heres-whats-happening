import { style } from '@vanilla-extract/css';
import { border, color, fontSize, fontWeight } from '../styles/theme';
import { phone } from '../styles/breakpoints.css';

export const venueDetail = style({
  display: 'flex',
  justifyContent: 'space-between',
  alignItems: 'flex-start',
  gap: '1rem',
  padding: '1rem 1.5rem',
  backgroundColor: color.whiteA70,
  border: border.sm,
  '@media': {
    [phone]: { padding: '1rem' },
  },
});

export const main = style({
  minWidth: 0,
  display: 'flex',
  flexDirection: 'column',
  gap: '0.25rem',
});

export const name = style({
  ...fontSize['2xl'],
  fontWeight: fontWeight.semibold,
  overflowWrap: 'anywhere',
  '@media': {
    [phone]: fontSize.xl,
  },
});

export const address = style({
  ...fontSize.sm,
  color: color.gray600,
});

export const links = style({
  display: 'flex',
  flexWrap: 'wrap',
  columnGap: '1rem',
  rowGap: '0.25rem',
  marginTop: '0.5rem',
});

export const stats = style({
  flexShrink: 0,
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'flex-end',
  textAlign: 'right',
});

export const count = style({
  ...fontSize.xl,
  fontWeight: fontWeight.bold,
  color: color.blue800,
});

export const countLabel = style({
  ...fontSize.xs,
  textTransform: 'uppercase',
  letterSpacing: '0.05em',
  color: color.gray500,
});

export const next = style({
  ...fontSize.xs,
  marginTop: '0.5rem',
  color: color.gray600,
  whiteSpace: 'nowrap',
});
