import { style, keyframes } from '@vanilla-extract/css';

const wave = keyframes({
  '0%': { transform: 'translateX(-100%)' },
  // +0.5s of delay between each loop
  '50%': { transform: 'translateX(100%)' },
  '100%': { transform: 'translateX(100%)' },
});

export const Skeleton = style({
  position: 'relative',
  background: 'transparent',
  boxSizing: 'border-box',
  maxWidth: '100%',
  maxHeight: '100%',
});

export const button = style({
  width: '100px',
  height: '40px',
  flex: '0 0 100px',
  display: 'inline-block',
});

export const input = style({
  height: '40px',
  width: '100%',
  display: 'inline-block',
});

export const h1 = style({
  height: '60px',
  width: '100%',
  maxWidth: '600px',
  padding: '15px',
});

export const text = style({
  lineHeight: '100%',
  fontSize: 'inherit',
  width: '100%',
  marginBottom: '4px',
});

export const fill = style({
  height: '100%',
  width: '100%',
  position: 'absolute',
  top: 0,
  left: 0,
  display: 'block',
});

export const fillRelative = style({
  position: 'relative',
});

export const fillInline = style({
  height: '100%',
  width: '100%',
  display: 'inline-block',
});

export const Animation = style({
  display: 'block',
  backgroundColor: 'rgba(0, 0, 0, 0.1)',
  overflow: 'hidden',
  position: 'relative',
  height: '100%',
  width: '100%',
  borderRadius: '4px',
  selectors: {
    '&::after': {
      top: 0,
      left: 0,
      right: 0,
      bottom: 0,
      content: '""',
      position: 'absolute',
      animation: `${wave} 1.6s linear 0.5s infinite`,
      transform: 'translateX(-100%)',
      background: 'linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.6), transparent)',
    },
    [`${button} &`]: {
      borderRadius: '20px',
    },
  },
});

// Children primarily used for sizing
export const Children = style({
  visibility: 'hidden',
});
