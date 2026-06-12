import { style } from '@vanilla-extract/css';
import { sectionTitle } from '../styles/common.css';
import { color, radius, shadow, fontSize } from '../styles/theme';

// fixed inset-0 z-50 flex items-center justify-center bg-black/40
export const backdrop = style({
  position: 'fixed',
  inset: 0,
  zIndex: 50,
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  backgroundColor: color.blackA40,
});

// w-[400px] max-w-[90%] rounded bg-white p-6 shadow-lg
export const dialog = style({
  width: '400px',
  maxWidth: '90%',
  borderRadius: radius.sm,
  backgroundColor: color.white,
  padding: '1.5rem',
  boxShadow: shadow.lg,
});

// mb-2 text-lg font-medium  (= sectionTitle + mb-2)
export const title = style([sectionTitle, { marginBottom: '0.5rem' }]);

// text-sm text-gray-700
export const message = style({ ...fontSize.sm, color: color.gray700 });

// mt-6 flex justify-center gap-3
export const actions = style({
  marginTop: '1.5rem',
  display: 'flex',
  justifyContent: 'center',
  gap: '0.75rem',
});
