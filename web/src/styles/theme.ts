export const color = {
  white: '#ffffff',
  whiteA60: 'rgb(255 255 255 / 0.6)',
  blackA40: 'rgb(0 0 0 / 0.4)',
  gray50: '#f9fafb',
  gray100: '#f3f4f6',
  gray200: '#e5e5e5',
  gray300: '#cbcbcb',
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
  yellow100: '#fffdea',
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
  sm: 0,
  full: '9999px',
} as const;

export const shadow = {
  sm: `3px 3px 0px 0 ${color.gray300}`,
  md: `5px 5px 0px 0 ${color.gray300}`,
  lg: `9px 9px 0px 0 ${color.gray300}`,
  hover: `5px 5px 2px 2px ${color.gray300};`,
} as const;

export const border = {
  sm: `1px solid ${color.gray300}`,
  md: `2px solid ${color.gray300}`,
  lg: `3px solid ${color.gray300}`,
} as const;

export const transition = {
  transitionProperty:
    'color, background-color, border-color, text-decoration-color, fill, stroke, opacity, box-shadow, transform, filter, backdrop-filter',
  transitionTimingFunction: 'cubic-bezier(0.4, 0, 0.2, 1)',
  transitionDuration: '150ms',
} as const;

export const textStroke = (width: string) =>
  ({
    WebkitTextStroke: `${width} ${color.white}`,
    paintOrder: 'stroke fill',
  }) as const;
