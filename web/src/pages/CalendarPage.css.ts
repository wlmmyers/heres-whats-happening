import { style } from '@vanilla-extract/css';
import { card } from '../styles/common.css';
import { color, radius, fontSize, fontWeight, transition, textStroke } from '../styles/theme';
import { phone } from '../styles/breakpoints.css';

export const controls = style({
  display: 'flex',
  alignItems: 'center',
  gap: '0.5rem',
  '@media': {
    [phone]: {
      display: 'none',
    },
  },
});

export const controlLabel = style({
  ...fontSize.sm,
  color: color.gray500,
  ...textStroke('4px'),
});

export const rangeButton = style({
  borderRadius: radius.sm,
  paddingInline: '0.75rem',
  paddingBlock: '0.25rem',
  ...fontSize.sm,
  fontWeight: fontWeight.medium,
  ...transition,
  whiteSpace: 'nowrap',
});

// The selected item is indicated by HorizontalSelector's fill (which also whitens
// the active text), so only the inactive colour is set here.
export const rangeButtonInactive = style({
  color: color.gray600,
  ...textStroke('4px'),
  ':hover': { color: color.gray900 },
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

export const inlineLink = style({
  color: color.blue600,
  textDecorationLine: 'underline',
});

export const list = style({ marginTop: '1rem' });
export const listCondensed = style({
  display: 'flex',
  flexWrap: 'wrap',
  // Make up for flex styling forcing the sectionTitleListItem to take up more height
  transform: 'translateY(-24px)',
});

export const listItem = style({
  marginBottom: '0.75rem',
  container: 'calendarListItem / inline-size',
});
export const listItemCondensed = style({
  minWidth: 'calc(33% - 1rem)',
  marginRight: '1rem',
});

export const banner = style([
  card,
  {
    display: 'flex',
    padding: '1rem',
    margin: '1rem 0',
    color: color.gray600,
    backgroundColor: color.yellow100,
    ...fontSize.sm,
  },
]);

export const sectionTitleListItem = style({
  width: '100%',
  flex: 'none',
});

export const notInterestedMessage = style([
  controlLabel,
  {
    marginTop: '1rem',
  },
]);

// The calendar list and the going sidebar side by side. Sidebar hidden on phones for now.
export const calendarWithSidebar = style({
  display: 'flex',
  alignItems: 'flex-start',
  gap: '3rem',
  '@media': {
    [phone]: {
      gap: 0,
    },
  },
});

// min-width: 0 or the calendar refuses to shrink below its cards' intrinsic
// width and pushes the sidebar off the page instead of reflowing.
export const calendarColumn = style({
  flex: 1,
  minWidth: 0,
});

export const sidebarColumn = style({
  // The calendar list beside this is many screens tall, so the panel rides
  // along rather than scrolling away at the top of it.
  position: 'sticky',
  top: '6rem',
  flex: '0 0 17rem',
  marginTop: '1.25rem',
  '@media': {
    [phone]: {
      display: 'none',
    },
  },
});
