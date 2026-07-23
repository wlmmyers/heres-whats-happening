import { style } from '@vanilla-extract/css';
import { buttonPrimary, errorText } from '../styles/common.css';
import { color, fontSize, radius } from '../styles/theme';

export const lead = style({
  color: color.gray600,
  marginTop: '0.5rem',
  marginBottom: '1rem',
  fontSize: fontSize.sm.fontSize,
});

export const inlineLink = style({
  color: color.blue600,
  textDecorationLine: 'underline',
});

export const error = style([errorText, { marginTop: '0.75rem' }]);

export const continueButton = style([buttonPrimary, { marginTop: '1.5rem' }]);

export const groupHeading = style({
  ...fontSize.sm,
  color: color.gray600,
  marginTop: '1rem',
  marginBottom: '0.5rem',
});

export const sectionHeading = style({
  ...fontSize.lg,
  fontWeight: 600,
});

export const showAllButton = style({
  marginTop: '0.5rem',
  color: color.blue600,
  textDecorationLine: 'underline',
  ...fontSize.sm,
});

export const emptyNote = style({
  color: color.gray600,
  ...fontSize.sm,
});

export const connectButton = style({
  backgroundColor: color.green600,
  color: color.white,
  borderRadius: radius.sm,
  paddingInline: '1rem',
  paddingBlock: '0.5rem',
  selectors: {
    '&:hover': { backgroundColor: color.green700 },
    '&:disabled': { opacity: 0.6 },
  },
});

export const continueSection = style({
  marginTop: '2rem',
  textAlign: 'center',
});
