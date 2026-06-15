import { style } from '@vanilla-extract/css';
import { color, radius } from '../styles/theme';

export const page = style({ minHeight: '100vh', backgroundColor: color.gray50 });

export const header = style({
  backgroundColor: color.white,
  borderBottomWidth: '1px',
  paddingInline: '1rem',
  paddingBlock: '0.75rem',
  display: 'flex',
  alignItems: 'center',
  gap: '0.5rem',
});

// backgroundImage stays inline (set in JSX)
export const logo = style({
  backgroundSize: 'cover',
  backgroundPosition: 'center',
  backgroundRepeat: 'no-repeat',
});

export const main = style({
  maxWidth: '64rem',
  marginInline: 'auto',
  paddingInline: '1rem',
  paddingBlock: '1.5rem',
});

export const navLink = style({
  paddingInline: '0.75rem',
  paddingBlock: '0.5rem',
  borderRadius: radius.sm,
});

export const navLinkActive = style({ backgroundColor: color.blue100, color: color.blue800 });

export const navLinkInactive = style({
  color: color.gray700,
  ':hover': { backgroundColor: color.gray100 },
});
