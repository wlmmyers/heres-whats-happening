import { style } from '@vanilla-extract/css';
import { card } from '../styles/common.css';
import { transition } from '../styles/theme';

export const skeletonCard = style([card, { position: 'relative', padding: '1rem', ...transition }]);
