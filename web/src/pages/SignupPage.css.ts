import { style } from '@vanilla-extract/css';
import { surface, errorText, buttonSubmit } from '../styles/common.css';
import { color, fontSize, fontWeight } from '../styles/theme';

export const page = style({
  minHeight: '100vh',
  backgroundColor: color.gray50,
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  paddingInline: '1rem',
});

export const form = style([
  surface,
  { width: '100%', maxWidth: '24rem', padding: '1.5rem' },
]);

export const title = style({ ...fontSize.xl, fontWeight: fontWeight.semibold });

export const field = style({ display: 'block', ...fontSize.sm, marginTop: '1rem' });

export const fieldLabel = style({ color: color.gray700 });

export const error = style([errorText, { marginTop: '1rem' }]);

export const submit = style([buttonSubmit, { marginTop: '1rem' }]);

export const switchText = style({ ...fontSize.sm, color: color.gray600, marginTop: '1rem' });

export const switchLink = style({
  color: color.blue600,
  ':hover': { textDecorationLine: 'underline' },
});
