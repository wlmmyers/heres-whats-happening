import { style } from '@vanilla-extract/css';
import { color, radius } from '../styles/theme';

export const container = style({
  position: 'relative',
  // Own stacking context so the indicator/item z-indexes stay scoped here and don't
  // interact with sibling elements (e.g. other header controls).
  isolation: 'isolate',
  display: 'flex',
  alignItems: 'center',
  gap: '0.5rem',
});

// Wrapper around each item. Holds the measurement ref and shrink-wraps its content
// so the indicator can hug it exactly. Sits above the indicator (which has a lower
// z-index) so a fill never covers the item's content. When onSelect is used this
// renders as a <button>; the global reset makes buttons visually neutral.
export const item = style({
  position: 'relative',
  zIndex: 1,
  display: 'flex',
  alignItems: 'center',
});

// fill mode only: the active item sits over the dark fill, so its text is forced
// white for legibility. Content inherits this colour, so item content should not
// hard-code its own colour when using fill mode.
export const itemFillActive = style({
  color: color.white,
});

// The sliding indicator that hugs the active item. Absolutely positioned and behind
// the items (zIndex 0) so a fill paints under their content; motion animates its
// x/width. box-sizing is border-box globally, so width === the item's offsetWidth
// makes an outline sit exactly on the item's box.
export const indicator = style({
  position: 'absolute',
  top: 0,
  bottom: 0,
  left: 0,
  borderRadius: radius.sm,
  pointerEvents: 'none',
  zIndex: 0,
});

// itemStyle="outline": a border around the active item, transparent centre.
export const indicatorOutline = style({
  borderWidth: '2px',
  borderStyle: 'solid',
  borderColor: color.blue800,
});

// itemStyle="fill": a solid blue box behind the active item.
export const indicatorFill = style({
  backgroundColor: color.blue800,
});
