import { style } from '@vanilla-extract/css';
import { clickableCard } from '../styles/common.css';
import { color, fontSize, fontWeight, transition } from '../styles/theme';
import { phone } from '../styles/breakpoints.css';

const cardStylesCondensed = {
  gridTemplateColumns: '6rem auto auto',
  gridTemplateRows: 'auto',
  gridTemplateAreas: `
    'thumbnail main main'
    'matchScore matchScore actions'
  `,
  selectors: {
    '&:not(:has([data-thumbnail]))': {
      gridTemplateColumns: '0 auto auto',
    },
  },
};

const scoreStylesCondensed = {
  textAlign: 'left' as const,
  alignItems: 'flex-end',
  justifyContent: 'flex-start',
  ...fontSize.xs,
};

export const eventCard = style([
  clickableCard,
  {
    display: 'grid',
    gridTemplateColumns: '8rem auto auto',
    gridTemplateRows: 'auto',
    gridTemplateAreas: `
      'thumbnail main matchScore'
      'thumbnail main actions'
    `,
    padding: '1rem',
    ...transition,
    // Collapse the thumbnail column to 0 when no thumbnail is rendered in the DOM.
    selectors: {
      '&:not(:has([data-thumbnail]))': {
        gridTemplateColumns: '0 auto auto',
      },
    },
    '@container': {
      ['calendarListItem (width < 500px)']: {
        ...cardStylesCondensed,
        // minHeight: '220px', this bungles the layout when the screen is small with the Going list shown
        // Need to rethink it if re-implementing the condensed event card option
      },
    },
    '@media': {
      [phone]: {
        ...cardStylesCondensed,
        minHeight: 'auto !important',
      },
    },
  },
]);

export const shorterMinHeight = style({
  '@container': {
    ['calendarListItem (width < 500px)']: {
      ...cardStylesCondensed,
      minHeight: '180px !important',
    },
  },
});

export const main = style({
  gridArea: 'main',
});

// Sizes and places the thumbnail slot; ArtistImage fills it.
export const thumbnail = style({
  gridArea: 'thumbnail',
  flexShrink: 0,
  width: '7rem',
  height: '7rem',
  '@media': {
    [phone]: {
      width: '5rem',
      height: '5rem',
    },
  },
  '@container': {
    ['calendarListItem (width < 500px)']: {
      width: '5rem',
      height: '5rem',
    },
  },
});

export const title = style({
  fontWeight: fontWeight.semibold,
  color: color.gray900,
  ...fontSize.lg,
  lineHeight: '1.5rem',
  '@container': {
    ['calendarListItem (width < 500px)']: {
      ...fontSize.base,
    },
  },
});

export const score = style({
  gridArea: 'matchScore',
  display: 'flex',
  alignItems: 'flex-start',
  justifyContent: 'flex-end',
  color: color.gray500,
  whiteSpace: 'nowrap',
  textAlign: 'right',
  ...fontSize.sm,
  '@media': {
    [phone]: scoreStylesCondensed,
  },
  '@container': {
    ['calendarListItem (width < 500px)']: scoreStylesCondensed,
  },
});

export const date = style({
  color: color.gray700,
  marginTop: '0.25rem',
  ...fontSize.sm,
});

export const matched = style({
  color: color.blue700,
  marginTop: '0.5rem',
  ...fontSize.xs,
});

export const actions = style({
  gridArea: 'actions',
  display: 'flex',
  alignItems: 'flex-end',
  justifyContent: 'flex-end',
  marginTop: '0.75rem',
  gap: '0.5rem',
});
