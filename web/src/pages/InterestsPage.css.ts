import { style } from '@vanilla-extract/css';
import { surface, buttonPrimary, errorText } from '../styles/common.css';
import { color, fontSize } from '../styles/theme';

export const lead = style({ color: color.gray600, marginTop: '0.5rem' });

export const inlineLink = style({
  color: color.blue600,
  textDecorationLine: 'underline',
});

export const section = style([surface, { padding: '1rem', marginTop: '1.5rem' }]);

export const error = style([errorText, { marginTop: '0.75rem' }]);

export const continueButton = style([buttonPrimary, { marginTop: '1.5rem' }]);

export const groupHeading = style({
  ...fontSize.sm,
  color: color.gray600,
  marginTop: '1rem',
  marginBottom: '0.5rem',
});

export const sectionHeading = style({
  ...fontSize.sm,
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
