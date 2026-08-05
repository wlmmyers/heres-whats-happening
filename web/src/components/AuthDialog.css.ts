import { style } from '@vanilla-extract/css';
import { errorText, buttonSubmit } from '../styles/common.css';
import { color, fontSize, fontWeight } from '../styles/theme';
import { dialogCard, dialogWrapper } from './Dialog.css';

export const wrapper = style([dialogWrapper]);

export const authCard = style([dialogCard]);

export const form = style({
  width: '100%',
});

export const title = style({ ...fontSize.xl, fontWeight: fontWeight.semibold });
export const subtitle = style({
  ...fontSize.sm,
  color: color.gray600,
  marginBottom: '1rem',
  width: '100%',
});

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

export const aside = style({
  ...fontSize.xs,
  color: color.gray500,
  display: 'block',
  marginTop: '0.5rem',
});

export const aboutLinkSection = style({
  fontSize: '0.875rem',
  width: '100%',
  textAlign: 'right',
});
