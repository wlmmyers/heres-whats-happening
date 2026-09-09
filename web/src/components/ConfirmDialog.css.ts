import { style } from '@vanilla-extract/css';
import { sectionTitle } from '../styles/common.css';
import { color, fontSize } from '../styles/theme';

export const title = style([sectionTitle, { marginBottom: '0.5rem' }]);

export const message = style({ ...fontSize.sm, color: color.gray700 });
