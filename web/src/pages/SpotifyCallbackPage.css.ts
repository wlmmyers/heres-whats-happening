import { style } from '@vanilla-extract/css';
import { buttonSecondary } from '../styles/common.css';
import { color, fontSize, fontWeight } from '../styles/theme';

export const container = style({ textAlign: 'center', paddingBlock: '3rem' });

export const title = style({ ...fontSize.xl, fontWeight: fontWeight.semibold });

export const message = style({ color: color.gray600, marginTop: '0.5rem' });

export const backButton = style([buttonSecondary, { marginTop: '1rem' }]);
