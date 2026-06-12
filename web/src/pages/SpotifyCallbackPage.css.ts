import { style } from '@vanilla-extract/css';
import { buttonSecondary } from '../styles/common.css';
import { color, fontSize, fontWeight } from '../styles/theme';

// text-center py-12
export const container = style({ textAlign: 'center', paddingBlock: '3rem' });

// text-xl font-semibold
export const title = style({ ...fontSize.xl, fontWeight: fontWeight.semibold });

// text-gray-600 mt-2
export const message = style({ color: color.gray600, marginTop: '0.5rem' });

// mt-4 border rounded px-4 py-2 hover:bg-gray-50  (back to settings)
export const backButton = style([buttonSecondary, { marginTop: '1rem' }]);
