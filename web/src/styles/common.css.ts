import { style } from '@vanilla-extract/css';
import { color, radius, shadow, fontSize, fontWeight, border, textStroke } from './theme';
import { phone } from './breakpoints.css';

export const card = style({
  backgroundColor: color.white,
  boxShadow: shadow.sm,
  borderRadius: radius.sm,
  border: border.sm,
});

export const cardNoShadow = style({
  backgroundColor: color.white,
  borderRadius: radius.sm,
  border: border.sm,
});

export const cardTranslucent = style({
  backgroundColor: color.whiteA70,
  borderRadius: radius.sm,
  border: border.sm,
});

export const clickableCard = style([
  card,
  {
    cursor: 'pointer',
    selectors: {
      '&:hover': {
        transform: 'scale(1.01)',
        boxShadow: shadow.hover,
      },
    },
  },
]);

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
  color: '#000',
  ...textStroke('10px'),
});

export const pageHeader = style({
  marginTop: '1rem',
  marginBottom: '2rem',
  display: 'flex',
  justifyContent: 'space-between',
  gap: '1rem',
  '@media': {
    [phone]: { flexDirection: 'column', marginTop: 0 },
  },
});

export const sectionTitle = style({
  ...fontSize.base,
  fontWeight: fontWeight.medium,
});

export const section = style([card, { padding: '1rem', margin: '1rem 0' }]);

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

export const bodySection = style({
  marginTop: '1rem',
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'flex-start',
  gap: '0.5rem',
});

export const link = style({
  color: color.blue600,
  textDecoration: 'underline',
});

export const linkButton = style({
  ...fontSize.sm,
  background: 'none',
  border: 'none',
  padding: 0,
  color: color.gray500,
  textDecoration: 'underline',
  cursor: 'pointer',
  selectors: {
    '&:hover': { color: color.gray700 },
  },
});
