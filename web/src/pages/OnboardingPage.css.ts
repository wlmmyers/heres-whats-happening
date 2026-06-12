import { style } from '@vanilla-extract/css';
import { surface, buttonPrimary, errorText } from '../styles/common.css';
import { color } from '../styles/theme';

// text-gray-600  (+ space-y-2 marginTop on the lead paragraph)
export const lead = style({ color: color.gray600, marginTop: '0.5rem' });

// text-blue-600 underline (inline link in lead)
export const inlineLink = style({ color: color.blue600, textDecorationLine: 'underline' });

// bg-white shadow rounded p-4 space-y-3  (+ space-y-6 marginTop)
export const section = style([surface, { padding: '1rem', marginTop: '1.5rem' }]);

// text-red-600 text-sm  (+ space-y-3 marginTop, since it follows TagInput in the section)
export const error = style([errorText, { marginTop: '0.75rem' }]);

// bg-blue-600 ... px-4 py-2  (Continue; + space-y-6 marginTop)
export const continueButton = style([buttonPrimary, { marginTop: '1.5rem' }]);
