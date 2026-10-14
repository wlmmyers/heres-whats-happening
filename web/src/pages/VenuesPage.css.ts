import { style } from '@vanilla-extract/css';
import { card } from '../styles/common.css';
import { color, fontSize, fontWeight, textStroke } from '../styles/theme';

export const selectorContainer = style({
  padding: '0.5rem',
  marginBottom: '1rem',
  backgroundColor: color.whiteA70,
});

export const selectorSkeleton = style([
  card,
  {
    position: 'relative',
    height: '3.25rem',
    marginBottom: '1rem',
  },
]);

export const selectorItem = style({
  display: 'flex',
  alignItems: 'center',
  gap: '0.5rem',
  minWidth: 0,
  padding: '0.5rem 0.75rem',
  whiteSpace: 'nowrap',
  color: color.gray700,
  justifyContent: 'space-between',
  width: '100%',
  ...fontSize.sm,
});

// The venue name truncates with an ellipsis when its selector column is too narrow.
export const selectorName = style({
  minWidth: 0,
  overflow: 'hidden',
  textOverflow: 'ellipsis',
});

export const selectorCount = style({
  flexShrink: 0,
  ...fontSize.xs,
  fontWeight: fontWeight.semibold,
  minWidth: '1.5rem',
  paddingInline: '0.375rem',
  borderRadius: '9999px',
  backgroundColor: color.gray100,
  color: color.gray600,
  textAlign: 'center',
});

export const eventsSection = style({
  marginTop: '1.5rem',
});

export const errorBox = style({ color: color.red600, marginTop: '1rem', ...textStroke('4px') });

export const emptyState = style([
  card,
  {
    padding: '2rem',
    textAlign: 'center',
    color: color.gray600,
    marginTop: '1rem',
  },
]);

export const horizontalSelector = style({
  gap: 0,
});
