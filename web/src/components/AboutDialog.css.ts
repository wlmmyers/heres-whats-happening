import { style } from '@vanilla-extract/css';
import { dialogCard, dialogWrapper } from './Dialog.css';
import { phone } from '../styles/breakpoints.css';

export const aboutDialogWrapper = style([
  dialogWrapper,
  {
    alignItems: 'flex-start',
    overflow: 'auto',
  },
]);

export const aboutCard = style([
  dialogCard,
  {
    width: '100%',
    maxWidth: '35rem',
    marginTop: '100px',
    marginBottom: '100px',
    '@media': {
      [phone]: {
        marginTop: '20px',
      },
    },
  },
]);

export const backLinkSection = style({
  width: '100%',
});

export const backLinkSectionBottom = style({
  width: '100%',
  textAlign: 'right',
});
