import { style } from '@vanilla-extract/css';
import { color, radius } from '../styles/theme';

// min-h-screen bg-gray-50
export const page = style({ minHeight: '100vh', backgroundColor: color.gray50 });

// bg-white border-b px-4 py-3 flex items-center gap-2
export const header = style({
  backgroundColor: color.white,
  borderBottomWidth: '1px',
  paddingInline: '1rem',
  paddingBlock: '0.75rem',
  display: 'flex',
  alignItems: 'center',
  gap: '0.5rem',
});

// bg-cover bg-center bg-no-repeat (logo div; backgroundImage stays inline)
export const logo = style({
  backgroundSize: 'cover',
  backgroundPosition: 'center',
  backgroundRepeat: 'no-repeat',
});

// max-w-5xl mx-auto px-4 py-6
export const main = style({
  maxWidth: '64rem',
  marginInline: 'auto',
  paddingInline: '1rem',
  paddingBlock: '1.5rem',
});

// NavLink base: px-3 py-2 rounded
export const navLink = style({
  paddingInline: '0.75rem',
  paddingBlock: '0.5rem',
  borderRadius: radius.sm,
});

// active: bg-blue-100 text-blue-800
export const navLinkActive = style({ backgroundColor: color.blue100, color: color.blue800 });

// inactive: text-gray-700 hover:bg-gray-100
export const navLinkInactive = style({
  color: color.gray700,
  ':hover': { backgroundColor: color.gray100 },
});
