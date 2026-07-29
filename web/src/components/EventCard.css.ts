import { style } from '@vanilla-extract/css';
import { clickableCard } from '../styles/common.css';
import { color, fontSize, fontWeight, radius, transition } from '../styles/theme';
import { phone } from '../styles/breakpoints.css';

const cardStylesCondensed = {
  gridTemplateColumns: '6rem auto auto',
  gridTemplateRows: 'auto',
  gridTemplateAreas: `
    'thumbnail main main'
    'matchScore matchScore notInterested'
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
      'thumbnail main notInterested'
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
        minHeight: '220px',
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

export const main = style({
  gridArea: 'main',
});

export const thumbnail = style({
  gridArea: 'thumbnail',
  flexShrink: 0,
  width: '7rem',
  height: '7rem',
  marginBottom: '0.5rem',
  objectFit: 'cover',
  borderRadius: radius.sm,
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

export const notInterested = style({
  gridArea: 'notInterested',
  display: 'flex',
  alignItems: 'flex-end',
  justifyContent: 'flex-end',
  marginTop: '0.75rem',
});

export const notInterestedButton = style({
  gridArea: 'notInterestedButton',
  fontWeight: fontWeight.medium,
  border: '1px solid',
  borderColor: color.gray200,
  borderRadius: radius.sm,
  paddingInline: '0.5rem',
  paddingBlock: '0.25rem',
  backgroundColor: color.white,
  ...transition,
  ...fontSize.xs,
  color: color.gray500,
  ':hover': { color: color.red600, borderColor: color.red600 },
});
