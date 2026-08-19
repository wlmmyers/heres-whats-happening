import { style } from '@vanilla-extract/css';
import { dialogCard } from './Dialog.css';
import { screen } from '../styles/common.css';
import { color, fontSize, fontWeight, radius, shadow, transition } from '../styles/theme';

export const container = style({
  position: 'relative',
  lineHeight: 0,
});

export const image = style({
  width: '100%',
  height: '100%',
  objectFit: 'cover',
  borderRadius: radius.sm,
});

export const creditButton = style({
  position: 'absolute',
  right: '0.25rem',
  bottom: '0.25rem',
  display: 'flex',
  width: '1.125rem',
  height: '1.125rem',
  padding: 0,
  border: 'none',
  borderRadius: radius.full,
  backgroundColor: color.whiteA70,
  cursor: 'pointer',
  ...transition,
  ':hover': { backgroundColor: color.white, transform: 'scale(1.15)' },
});

export const creditIcon = style({
  width: '100%',
  height: '100%',
});

// `screen` sits at zIndex 2, under the user menu; dialogs in this app sit at 50.
export const creditBackdrop = style([screen, { zIndex: 50 }]);

export const creditCard = style([
  dialogCard,
  {
    width: '26rem',
    maxWidth: '90%',
    alignItems: 'stretch',
    // The card renders inside a click-to-navigate event card; keep the cursor
    // from advertising a click that goes nowhere.
    cursor: 'auto',
    boxShadow: shadow.md,
  },
]);

export const creditTitle = style({
  ...fontSize.base,
  fontWeight: fontWeight.medium,
  marginBottom: '0.75rem',
});

export const creditList = style({
  display: 'grid',
  gridTemplateColumns: 'auto 1fr',
  gap: '0.25rem 0.75rem',
  ...fontSize.sm,
});

export const creditLabel = style({
  color: color.gray500,
  whiteSpace: 'nowrap',
});

export const creditValue = style({
  color: color.gray800,
  overflowWrap: 'anywhere',
});

export const creditNote = style({
  ...fontSize.xs,
  color: color.gray500,
  marginTop: '0.75rem',
});

export const creditActions = style({
  marginTop: '1.5rem',
  display: 'flex',
  justifyContent: 'flex-end',
});
