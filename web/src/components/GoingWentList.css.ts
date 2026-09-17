import { style } from '@vanilla-extract/css';
import { cardTranslucent } from '../styles/common.css';
import { color, fontSize, fontWeight, textStroke, transition } from '../styles/theme';
import { phone } from '../styles/breakpoints.css';

export const goingWentList = style({
  alignSelf: 'flex-start',
  '@media': {
    [phone]: {
      position: 'static',
      maxHeight: 'none',
    },
  },
});

export const innerContainer = style([
  cardTranslucent,
  {
    marginTop: '1rem',
    maxHeight: 'calc(100vh - 12rem)',
    overflowY: 'auto',
  },
]);

export const noBorder = style({
  border: 'none',
});

export const heading = style({
  display: 'flex',
  alignItems: 'baseline',
  justifyContent: 'space-between',
  gap: '0.5rem',
  marginBottom: '0.75rem',
  ...textStroke('6px'),
});

export const count = style({
  ...fontSize.xs,
  color: color.gray500,
  ...textStroke('4px'),
});

export const item = style({
  display: 'flex',
  alignItems: 'baseline',
  gap: '0.5rem',
  padding: '0.625rem',
  cursor: 'pointer',
  ...transition,
  selectors: {
    // Separators between rows only, so the list does not open on a rule that
    // would double up with the heading's spacing.
    '& + &': { borderTop: `1px solid ${color.gray200}` },
    '&:hover': { backgroundColor: color.gray50 },
  },
});

// A hand-added row has no detail page to open, so it must not offer the
// pointer or the hover lift that promise one.
export const itemStatic = style({
  cursor: 'default',
  position: 'relative',
  paddingLeft: '1.25rem',
  selectors: {
    '&:hover': { backgroundColor: 'transparent' },
    '&::before': {
      position: 'absolute',
      content: '',
      left: '0.5rem',
      width: '4px',
      height: 'calc(100% - 1rem)',
      backgroundColor: color.blue200,
      marginRight: '0.5rem',
    },
  },
});

export const manualLabel = style({
  ...fontSize.xs,
  display: 'inline-block',
  color: color.blue300,
  fontWeight: fontWeight.light,
  marginLeft: '0.5rem',
  opacity: 0,
  selectors: {
    [`${item}:hover &`]: { opacity: 1 },
  },
});

export const itemMain = style({
  // Without this a long title stretches the flex item and pushes the remove
  // button out of the panel instead of wrapping.
  minWidth: 0,
  flex: 1,
});

export const itemDate = style({
  ...fontSize.xs,
  color: color.gray500,
  fontWeight: fontWeight.medium,
});

export const itemTitle = style({
  ...fontSize.sm,
  fontWeight: fontWeight.semibold,
  color: color.gray900,
  marginTop: '0.125rem',
});

export const itemVenue = style({
  ...fontSize.xs,
  color: color.gray600,
  marginTop: '0.125rem',
});

export const removeButton = style({
  flexShrink: 0,
  ...fontSize.xs,
  background: 'none',
  border: 'none',
  padding: 0,
  color: color.gray500,
  cursor: 'pointer',
  // Hidden until the row is hovered or the button is keyboard-focused, so the
  // list reads as shows rather than as a column of controls.
  opacity: 0,
  ...transition,
  selectors: {
    [`${item}:hover &`]: { opacity: 1 },
    '&:focus-visible': { opacity: 1 },
    '&:hover': { color: color.red600 },
  },
  '@media': {
    [phone]: { opacity: 1 },
  },
});

export const emptyText = style({
  ...fontSize.sm,
  padding: '0.625rem',
  color: color.gray600,
});

export const helperText = style({
  display: 'block',
  marginTop: '0.5rem',
  color: color.gray500,
  ...fontSize.xs,
});

export const errorText = style({
  ...fontSize.sm,
  padding: '0.625rem',
  color: color.red600,
});

export const skeletonRow = style({
  position: 'static',
  height: '3.25rem',
  backgroundColor: color.gray100,
  selectors: {
    '&:not(:last-child)': {
      marginBottom: '0.5rem',
    },
  },
});

export const wentToggle = style({
  display: 'flex',
  alignItems: 'center',
  gap: '8px',
});

export const wentHeading = style({
  marginTop: '1.25rem',
});

export const wentSectionTitle = style({});

// Placement only; the caret's size and colour come from RotatingCaret.
export const wentCaret = style({
  // Pushed to the far edge, where a disclosure caret is looked for.
  marginLeft: 'auto',
});

// Clips the past rows while their height animates between 0 and auto.
export const wentBody = style({
  overflow: 'hidden',
});

// Sits at the far edge of the Past Shows heading, opposite the title. The
// toggle beside it is a button too, so this one is a sibling rather than a
// child.
export const addButton = style({
  flexShrink: 0,
  marginLeft: 'auto',
  padding: '0 0.25rem',
  ...fontSize.sm,
  lineHeight: 1,
  color: color.gray500,
  cursor: 'pointer',
  ...transition,
  selectors: {
    '&:hover': { color: color.gray900 },
  },
});
