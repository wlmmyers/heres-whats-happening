import { style } from '@vanilla-extract/css';
import { color, radius, textStroke } from '../styles/theme';
import { phone } from '../styles/breakpoints.css';

export const page = style({
  minHeight: '100vh',
  backgroundColor: color.gray50,
});

export const header = style({
  position: 'fixed',
  backgroundColor: color.white,
  borderBottomWidth: '1px',
  padding: '0 1rem',
  display: 'flex',
  alignItems: 'center',
  gap: '0.5rem',
  top: 0,
  right: 0,
  left: 0,
  zIndex: 1,
  height: '4rem',
});

export const hiddenOnPhone = style([
  {
    '@media': {
      [phone]: { display: 'none' },
    },
  },
]);

export const logo = style({
  backgroundSize: 'cover',
  backgroundPosition: 'center',
  backgroundRepeat: 'no-repeat',
  backgroundImage: `url('/titleGraphic1.png')`,
  width: '280px',
  height: '45px',
  transform: 'translateY(2px)',
  '@media': {
    [phone]: {
      backgroundImage: `url('/titleGraphicMobile.png')`,
      width: '100px',
      padding: '0.5rem 0',
      marginRight: '0.5rem',
      backgroundOrigin: 'content-box',
    },
  },
});

export const main = style({
  position: 'relative',
  maxWidth: '60rem',
  marginInline: 'auto',
  marginTop: '4rem',
  paddingInline: '1rem',
  paddingBlock: '1.5rem',
});

export const mainLoggedOut = style([
  main,
  {
    '@media': {
      [phone]: { marginTop: 0 },
    },
  },
]);

export const navLink = style({
  padding: '0.5rem 0.75rem',
  borderRadius: radius.sm,
  '@media': {
    [phone]: { padding: '0.5rem 0.5rem' },
  },
});

export const navLinkActive = style({
  color: color.blue800,
});

export const navLinkInactive = style({
  color: color.gray700,
  ':hover': { backgroundColor: color.gray100 },
});

export const authActions = style({
  marginLeft: 'auto',
  display: 'flex',
  gap: '0.5rem',
});

export const footer = style({
  marginTop: '2rem',
  paddingBlock: '1rem',
  textAlign: 'center',
  color: color.gray500,
  fontSize: '0.875rem',
  ...textStroke('4px'),
});

export const background = style({
  position: 'fixed',
  top: 0,
  right: 0,
  bottom: 0,
  left: 0,
  backgroundImage: 'repeating-linear-gradient(304deg, #ffffff 0 10px, #e5e5e5 10px 12px)',
  backgroundSize: 'cover',
  backgroundPosition: 'center',
  backgroundRepeat: 'no-repeat',
});
