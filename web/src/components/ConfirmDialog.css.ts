import { style } from '@vanilla-extract/css';
import { sectionTitle } from '../styles/common.css';
import { color, radius, shadow, fontSize } from '../styles/theme';

export const backdrop = style({
  position: 'fixed',
  inset: 0,
  zIndex: 50,
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  backgroundColor: color.blackA40,
});

export const dialog = style({
  width: '400px',
  maxWidth: '90%',
  borderRadius: radius.sm,
  backgroundColor: color.white,
  padding: '1.5rem',
  boxShadow: shadow.lg,
});

export const title = style([sectionTitle, { marginBottom: '0.5rem' }]);

export const message = style({ ...fontSize.sm, color: color.gray700 });

export const actions = style({
  marginTop: '1.5rem',
  display: 'flex',
  justifyContent: 'center',
  gap: '0.75rem',
});
