import { style } from '@vanilla-extract/css';
import { card, errorText, buttonSubmit } from '../styles/common.css';
import { color, fontSize, fontWeight } from '../styles/theme';

export const page = style({
  minHeight: '100vh',
  backgroundColor: color.gray50,
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'center',
  justifyContent: 'center',
  paddingInline: '1rem',
  '@media': {
    'screen and (min-width: 768px)': { flexDirection: 'row', gap: '2rem' },
  },
});

export const logo = style({
  marginBottom: '1rem',
  '@media': {
    'screen and (max-width: 768px)': { marginBottom: '2rem' },
  },
});

export const loginCard = style([
  card,
  {
    width: '100%',
    maxWidth: '24rem',
    padding: '1.5rem',
    display: 'flex',
    flexDirection: 'column',
    alignItems: 'center',
  },
]);

export const form = style([]);

export const title = style({ ...fontSize.xl, fontWeight: fontWeight.semibold });

export const field = style({
  display: 'block',
  ...fontSize.sm,
  marginTop: '1rem',
});

export const fieldLabel = style({ color: color.gray700 });

export const error = style([errorText, { marginTop: '1rem' }]);

export const submit = style([buttonSubmit, { marginTop: '1rem' }]);

export const switchText = style({
  ...fontSize.sm,
  color: color.gray600,
  marginTop: '1rem',
});

export const switchLink = style({
  color: color.blue600,
  ':hover': { textDecorationLine: 'underline' },
});
