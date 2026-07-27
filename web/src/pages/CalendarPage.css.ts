import { style } from '@vanilla-extract/css';
import { card } from '../styles/common.css';
import { color, radius, fontSize, fontWeight, transition, textStroke } from '../styles/theme';

export const controls = style({
  display: 'flex',
  alignItems: 'center',
  gap: '0.5rem',
});

export const controlLabel = style({
  ...fontSize.sm,
  color: color.gray500,
  ...textStroke('4px'),
});

export const segment = style({ display: 'inline-flex', padding: '0.125rem' });

export const rangeButton = style({
  borderRadius: radius.sm,
  paddingInline: '0.75rem',
  paddingBlock: '0.25rem',
  ...fontSize.sm,
  fontWeight: fontWeight.medium,
  ...transition,
  whiteSpace: 'nowrap',
});

export const rangeButtonActive = style({
  backgroundColor: color.blue600,
  color: color.white,
});

export const rangeButtonInactive = style({
  color: color.gray600,
  ':hover': { color: color.gray900 },
});

export const errorBox = style({ color: color.red600, marginTop: '1rem' });

export const emptyState = style([
  card,
  {
    padding: '2rem',
    textAlign: 'center',
    color: color.gray600,
    marginTop: '1rem',
  },
]);

export const inlineLink = style({
  color: color.blue600,
  textDecorationLine: 'underline',
});

export const list = style({ marginTop: '1rem' });

export const listItem = style({ marginBottom: '0.75rem' });
