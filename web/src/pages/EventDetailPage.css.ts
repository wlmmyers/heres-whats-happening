import { style } from '@vanilla-extract/css';
import { buttonPrimary } from '../styles/common.css';
import { color, radius, fontSize, fontWeight, textStroke } from '../styles/theme';
import { phone } from '../styles/breakpoints.css';

export const backLink = style({
  display: 'block',
  ...fontSize.sm,
  color: color.blue600,
  fontWeight: fontWeight.medium,
  marginBottom: '0.625rem',
  height: '30px',
  ...textStroke('4px'),
  ':hover': { textDecorationLine: 'underline' },
});

export const thumbnail = style({
  width: '9rem',
  height: '9rem',
  objectFit: 'cover',
  borderRadius: radius.sm,
  '@media': {
    [phone]: { width: '100%', height: 'auto' },
  },
});

export const detail = style({
  display: 'flex',
  position: 'relative',
  justifyContent: 'space-between',
  minHeight: '144px',
  '@media': {
    [phone]: { flexDirection: 'column' },
  },
});

export const detailText = style({
  minWidth: 0,
  flexGrow: 1,
  padding: '0.5rem 1rem',
  '@media': {
    [phone]: { padding: '1rem' },
  },
});

export const title = style({
  ...fontSize['2xl'],
  fontWeight: fontWeight.semibold,
});

export const date = style({ color: color.gray700, marginTop: '0.25rem' });

export const venue = style({ color: color.gray600, marginTop: '0.25rem' });

export const matched = style({
  ...fontSize.xs,
  color: color.gray500,
  marginTop: '0.5rem',
});

export const viewEventSection = style({
  display: 'flex',
  justifyContent: 'flex-end',
  margin: '1rem 0',
});

export const viewEventLink = style([buttonPrimary, { display: 'block' }]);

export const notFound = style({ color: color.gray700 });

export const setlistTitle = style({
  ...fontSize.sm,
  margin: '20px 0 5px 0',
  fontWeight: fontWeight.medium,
});

export const setlistInset = style({
  position: 'relative',
  padding: '10px 0 10px 18px',
  marginBottom: '10px',
  '::after': {
    content: '',
    position: 'absolute',
    top: '10px',
    left: 0,
    width: '4px',
    height: 'calc(100% - 20px)',
    backgroundColor: color.blue100,
  },
});

export const setlistLink = style({
  display: 'block',
  marginBottom: '6px',
});
export const setlistSongList = style({});

export const setlistObserved = style({
  ...fontSize.xs,
  marginBottom: '6px',
});

// --- Setlist spoiler guard ---
// Applied to setlistInset when the user has not opted into seeing setlists.
// The songs stay in the layout so the block keeps its size, but they are
// blurred and unselectable; the markup also marks them aria-hidden so the
// titles are never announced or copied.
export const setlistHidden = style({
  filter: 'blur(6px)',
  userSelect: 'none',
  pointerEvents: 'none',
});

export const setlistGuard = style({
  position: 'relative',
});

export const setlistOverlay = style({
  position: 'absolute',
  inset: 0,
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'center',
  justifyContent: 'center',
  gap: '0.25rem',
  textAlign: 'center',
  padding: '0.5rem',
  borderRadius: radius.sm,
  backgroundColor: color.whiteA70,
});

export const setlistOverlayText = style({
  ...fontSize.sm,
  fontWeight: fontWeight.medium,
  color: color.gray800,
});

export const setlistOverlayLink = style({
  ...fontSize.sm,
  color: color.blue600,
  fontWeight: fontWeight.medium,
  ':hover': { textDecorationLine: 'underline' },
});
