import { style } from '@vanilla-extract/css';
import { color, radius, shadow, fontSize, fontWeight } from '../styles/theme';

// relative ml-auto
export const root = style({ position: 'relative', marginLeft: 'auto' });

// w-8 h-8 rounded-full bg-blue-500 text-white text-sm font-semibold flex items-center justify-center cursor-pointer
export const avatar = style({
  width: '2rem',
  height: '2rem',
  borderRadius: radius.full,
  backgroundColor: color.blue500,
  color: color.white,
  ...fontSize.sm,
  fontWeight: fontWeight.semibold,
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  cursor: 'pointer',
});

// fixed inset-0 z-10
export const overlay = style({ position: 'fixed', inset: 0, zIndex: 10 });

// absolute top-full right-0 mt-1 z-20 min-w-max rounded border border-gray-200 bg-white px-3 py-2 shadow-md
export const dropdown = style({
  position: 'absolute',
  top: '100%',
  right: 0,
  marginTop: '0.25rem',
  zIndex: 20,
  minWidth: 'max-content',
  borderRadius: radius.sm,
  borderWidth: '1px',
  borderStyle: 'solid',
  borderColor: color.gray200,
  backgroundColor: color.white,
  paddingInline: '0.75rem',
  paddingBlock: '0.5rem',
  boxShadow: shadow.md,
});

// mb-2 text-xs text-gray-500
export const email = style({ marginBottom: '0.5rem', ...fontSize.xs, color: color.gray500 });

// mb-2 border-gray-100
export const divider = style({ marginBottom: '0.5rem', borderColor: color.gray100 });

// cursor-pointer border-0 bg-transparent p-0 text-sm text-gray-700
export const signOut = style({
  cursor: 'pointer',
  borderWidth: 0,
  backgroundColor: 'transparent',
  padding: 0,
  ...fontSize.sm,
  color: color.gray700,
});
