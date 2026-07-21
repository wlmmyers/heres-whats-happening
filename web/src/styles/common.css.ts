import { style } from '@vanilla-extract/css';
import { color, radius, shadow, fontSize, fontWeight } from './theme';

export const card = style({
  backgroundColor: color.white,
  boxShadow: shadow.base,
  borderRadius: radius.sm,
});

export const buttonPrimary = style({
  backgroundColor: color.blue600,
  color: color.white,
  borderRadius: radius.sm,
  paddingInline: '1rem',
  paddingBlock: '0.5rem',
  selectors: {
    '&:hover': { backgroundColor: color.blue700 },
    '&:disabled': { opacity: 0.6 },
  },
});

export const buttonSecondary = style({
  borderWidth: '1px',
  borderStyle: 'solid',
  borderRadius: radius.sm,
  paddingInline: '1rem',
  paddingBlock: '0.5rem',
  selectors: {
    '&:hover': { backgroundColor: color.gray50 },
    '&:disabled': { opacity: 0.6 },
  },
});

export const buttonSubmit = style({
  width: '100%',
  backgroundColor: color.blue600,
  color: color.white,
  borderRadius: radius.sm,
  paddingBlock: '0.5rem',
  selectors: {
    '&:hover': { backgroundColor: color.blue700 },
    '&:disabled': { opacity: 0.5 },
  },
});

export const textInput = style({
  marginTop: '0.25rem',
  width: '100%',
  borderWidth: '1px',
  borderStyle: 'solid',
  borderRadius: radius.sm,
  paddingInline: '0.5rem',
  paddingBlock: '0.375rem',
});

export const pageTitle = style({
  ...fontSize['2xl'],
  fontWeight: fontWeight.semibold,
});

export const sectionTitle = style({
  ...fontSize.base,
  fontWeight: fontWeight.medium,
});

export const section = style([card, { padding: '1rem', marginTop: '1.5rem' }]);

export const errorText = style({ ...fontSize.sm, color: color.red600 });

export const screen = style({
  position: 'fixed',
  inset: 0,
  backgroundColor: color.blackA40,
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  zIndex: 2,
});
