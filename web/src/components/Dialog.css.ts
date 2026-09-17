import { style } from '@vanilla-extract/css';
import { cardNoShadow } from '../styles/common.css';
import { color, fontSize, radius } from '../styles/theme';
import { phone } from '../styles/breakpoints.css';

export const authDialogWrapper = style({
  position: 'fixed',
  display: 'flex',
  justifyContent: 'center',
  alignItems: 'center',
  top: 0,
  left: 0,
  zIndex: 50,
  width: '100%',
  height: '100%',
  paddingInline: '1rem',
});

export const backdrop = style({
  position: 'fixed',
  inset: 0,
  zIndex: 50,
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  backgroundColor: color.blackA40,
});

export const topOnPhone = style({
  '@media': {
    /* Move to the top on small screens so the dialog is not hidden behind the keyboard. */
    [phone]: { alignItems: 'flex-start', paddingTop: 80 },
  },
});

export const dialogCard = style([
  cardNoShadow,
  {
    width: '24em',
    padding: '1.5rem',
    display: 'flex',
    flexDirection: 'column',
    alignItems: 'center',
  },
]);

export const dialog = style([
  cardNoShadow,
  {
    width: '400px',
    maxWidth: '90%',
    padding: '1.5rem',
  },
]);

export const body = style({
  ...fontSize.sm,
  color: color.gray700,
  marginTop: '0.5rem',
});

export const status = style({
  ...fontSize.sm,
  color: color.gray600,
  marginTop: '0.75rem',
  textAlign: 'right',
});

// Anchors the absolutely positioned close button to the dialog's corner.
export const closable = style({
  position: 'relative',
});

// Keeps the heading clear of the close button once a narrow screen wraps it.
export const heading = style({
  paddingRight: '2rem',
});

export const closeButton = style({
  position: 'absolute',
  top: '0.75rem',
  right: '0.75rem',
  width: '2rem',
  height: '2rem',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  background: 'none',
  border: 'none',
  borderRadius: radius.sm,
  padding: 0,
  cursor: 'pointer',
  fontSize: '2rem',
  fontWeight: 200,
  lineHeight: 1,
  color: color.gray400,
  selectors: {
    '&:hover': { color: color.gray700, backgroundColor: color.gray100 },
  },
});
