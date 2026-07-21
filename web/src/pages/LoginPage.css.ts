import { style } from '@vanilla-extract/css';
import { color } from '../styles/theme';

export const page = style({
  minHeight: '100vh',
  backgroundColor: color.gray50,
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'center',
  justifyContent: 'center',
  paddingInline: '1rem',
  '@media': {
    'screen and (min-width: 768px)': { flexDirection: 'row', gap: '2rem' },
  },
});
