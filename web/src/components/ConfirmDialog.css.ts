import { style } from '@vanilla-extract/css';
import { color, fontSize } from '../styles/theme';

export const message = style({ ...fontSize.sm, color: color.gray700, marginTop: '0.5rem' });
