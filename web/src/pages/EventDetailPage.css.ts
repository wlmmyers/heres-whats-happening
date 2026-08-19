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
  margin: '10px 0 5px 0',
  fontWeight: fontWeight.medium,
});

export const setlistInset = style({
  position: 'relative',
  padding: '10px 0 10px 15px',
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
});
