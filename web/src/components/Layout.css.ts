import { style } from '@vanilla-extract/css';
import { color, radius } from '../styles/theme';

export const page = style({
  minHeight: '100vh',
  backgroundColor: color.gray50,
});

export const header = style({
  backgroundColor: color.white,
  borderBottomWidth: '1px',
  padding: '0 1rem',
  display: 'flex',
  alignItems: 'center',
  gap: '0.5rem',
  position: 'fixed',
  top: 0,
  right: 0,
  left: 0,
  zIndex: 1,
  height: '4rem',
});

// backgroundImage stays inline (set in JSX)
export const logo = style({
  backgroundSize: 'cover',
  backgroundPosition: 'center',
  backgroundRepeat: 'no-repeat',
});

export const main = style({
  maxWidth: '60rem',
  marginInline: 'auto',
  marginTop: '4rem',
  paddingInline: '1rem',
  paddingBlock: '1.5rem',
});

export const navLink = style({
  paddingInline: '0.75rem',
  paddingBlock: '0.5rem',
  borderRadius: radius.sm,
});

export const navLinkActive = style({
  backgroundColor: color.blue100,
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
