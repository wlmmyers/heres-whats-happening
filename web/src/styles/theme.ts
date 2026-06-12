// Design tokens extracted from Tailwind's default theme. Spacing is intentionally
// NOT tokenized — padding/margin/gap are written as literal rem values at each
// use site. Font sizes are spreadable objects: `style({ ...fontSize.lg })`.

export const color = {
  white: '#ffffff',
  black: '#000000',
  blackA40: 'rgb(0 0 0 / 0.4)',
  gray50: '#f9fafb',
  gray100: '#f3f4f6',
  gray200: '#e5e7eb',
  gray500: '#6b7280',
  gray600: '#4b5563',
  gray700: '#374151',
  gray800: '#1f2937',
  gray900: '#111827',
  blue50: '#eff6ff',
  blue100: '#dbeafe',
  blue500: '#3b82f6',
  blue600: '#2563eb',
  blue700: '#1d4ed8',
  blue800: '#1e40af',
  blue900: '#1e3a8a',
  green600: '#16a34a',
  green700: '#15803d',
  red600: '#dc2626',
} as const;

export const fontSize = {
  xs: { fontSize: '0.75rem', lineHeight: '1rem' },
  sm: { fontSize: '0.875rem', lineHeight: '1.25rem' },
  base: { fontSize: '1rem', lineHeight: '1.5rem' },
  lg: { fontSize: '1.125rem', lineHeight: '1.75rem' },
  xl: { fontSize: '1.25rem', lineHeight: '1.75rem' },
  '2xl': { fontSize: '1.5rem', lineHeight: '2rem' },
  '3xl': { fontSize: '1.875rem', lineHeight: '2.25rem' },
} as const;

export const fontWeight = {
  medium: 500,
  semibold: 600,
  bold: 700,
} as const;

export const radius = {
  sm: '0.25rem',
  md: '0.375rem',
  full: '9999px',
} as const;

export const shadow = {
  sm: '0 1px 2px 0 rgb(0 0 0 / 0.05)',
  base: '0 1px 3px 0 rgb(0 0 0 / 0.1), 0 1px 2px -1px rgb(0 0 0 / 0.1)',
  md: '0 4px 6px -1px rgb(0 0 0 / 0.1), 0 2px 4px -2px rgb(0 0 0 / 0.1)',
  lg: '0 10px 15px -3px rgb(0 0 0 / 0.1), 0 4px 6px -4px rgb(0 0 0 / 0.1)',
} as const;

// Tailwind's `transition` utility. Spread into a style: `style({ ...transition })`.
export const transition = {
  transitionProperty:
    'color, background-color, border-color, text-decoration-color, fill, stroke, opacity, box-shadow, transform, filter, backdrop-filter',
  transitionTimingFunction: 'cubic-bezier(0.4, 0, 0.2, 1)',
  transitionDuration: '150ms',
} as const;
