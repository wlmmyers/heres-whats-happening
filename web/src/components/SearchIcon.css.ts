import { style } from '@vanilla-extract/css';
import { color } from '../styles/theme';

const lineWidth = 2;
// Same meaning as textStroke's width: the stroke is centred on the line's edge,
// so half of it shows on each side.
const outlineWidth = 5;

export const searchIcon = style({
  width: '20px',
  marginRight: '10px',
  // Let the outline spill past the viewBox, as a text stroke spills past its
  // glyph, instead of clipping it or shrinking the icon to make room.
  overflow: 'visible',
  fill: 'none',
  strokeMiterlimit: 10,
});

export const outline = style({
  stroke: color.white,
  strokeWidth: lineWidth + outlineWidth,
  // Carries the outline around the handle's end, which a butt cap leaves bare.
  strokeLinecap: 'square',
});

export const glyph = style({
  stroke: 'currentColor',
  strokeWidth: lineWidth,
});
