import { style } from '@vanilla-extract/css';
import { cardNoShadow } from '../styles/common.css';

export const dialogWrapper = style({
  position: 'fixed',
  display: 'flex',
  justifyContent: 'center',
  alignItems: 'center',
  top: 0,
  left: 0,
  zIndex: 50,
  width: '100%',
  height: '100%',
  paddingInline: '1rem',
});

export const dialogCard = style([
  cardNoShadow,
  {
    width: '24em',
    padding: '1.5rem',
    display: 'flex',
    flexDirection: 'column',
    alignItems: 'center',
  },
]);
