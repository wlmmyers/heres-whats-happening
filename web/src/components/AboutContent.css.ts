import { style } from '@vanilla-extract/css';
import { color, fontSize, fontWeight } from '../styles/theme';

export const logoWrap = style({
  maxWidth: '320px',
  marginInline: 'auto',
});

export const lede = style({
  ...fontSize.base,
  color: color.gray700,
  textAlign: 'center',
  maxWidth: '34rem',
  marginInline: 'auto',
  marginBottom: '1rem',
});

export const birds = style({
  display: 'flex',
  justifyContent: 'center',
  gap: '0.5rem',
  marginTop: '2rem',
  marginBottom: '2rem',
});

export const bird = style({
  width: '3rem',
  height: '3rem',
});

export const list = style({ margin: '1rem 0 2rem' });

// Hairline between consecutive rows, never above the first one.
const dividedRow = style({
  selectors: {
    '& + &': {
      marginTop: '0.75rem',
      paddingTop: '0.75rem',
      borderTop: `1px solid ${color.gray100}`,
    },
  },
});

export const featureRow = style([dividedRow]);

export const comingRow = style([dividedRow]);

export const emoji = style({
  ...fontSize.xl,
  flex: '0 0 auto',
  lineHeight: 1.2,
});

export const rowName = style({
  ...fontSize.base,
  fontWeight: fontWeight.semibold,
  display: 'block',
});

export const rowText = style({
  ...fontSize.sm,
  color: color.gray700,
});
