import { style } from '@vanilla-extract/css';
import { color, radius, fontSize } from '../styles/theme';

// border rounded p-2 flex flex-wrap gap-2 items-center
export const wrapper = style({
  borderWidth: '1px',
  borderStyle: 'solid',
  borderRadius: radius.sm,
  padding: '0.5rem',
  display: 'flex',
  flexWrap: 'wrap',
  gap: '0.5rem',
  alignItems: 'center',
});

// inline-flex items-center bg-blue-100 text-blue-800 rounded-full px-3 py-1 text-sm
export const tag = style({
  display: 'inline-flex',
  alignItems: 'center',
  backgroundColor: color.blue100,
  color: color.blue800,
  borderRadius: radius.full,
  paddingInline: '0.75rem',
  paddingBlock: '0.25rem',
  ...fontSize.sm,
});

// ml-2 text-blue-700 hover:text-red-600
export const removeButton = style({
  marginLeft: '0.5rem',
  color: color.blue700,
  ':hover': { color: color.red600 },
});

// flex-1 min-w-[120px] border-0 outline-none p-1 text-sm
export const input = style({
  flex: '1 1 0%',
  minWidth: '120px',
  borderWidth: 0,
  outline: 'none',
  padding: '0.25rem',
  ...fontSize.sm,
});
