import { style } from '@vanilla-extract/css';

export const externalLink = style({
  textDecoration: 'underline',
});

// The global stylesheet resets `img, svg, video, canvas` to `display: block`
// with `height: auto`, which would drop the icon onto its own line and collapse
// it. A class selector outranks that element selector, so both are restated.
export const icon = style({
  display: 'inline-block',
  width: '0.9em',
  height: '0.9em',
  marginLeft: '0.25em',
  verticalAlign: '-0.1em',
});

export const srOnly = style({
  position: 'absolute',
  width: '1px',
  height: '1px',
  padding: 0,
  margin: '-1px',
  border: 0,
  overflow: 'hidden',
  clip: 'rect(0, 0, 0, 0)',
  whiteSpace: 'nowrap',
});
