import { style } from '@vanilla-extract/css';

export const wrapper = style({
  position: 'fixed',
  top: '50%',
  left: '50%',
  transform: 'translate(-50%, -50%)',
  zIndex: 50,
  width: '100%',
  maxWidth: '24rem',
  paddingInline: '1rem',
});
