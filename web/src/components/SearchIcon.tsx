import * as s from './SearchIcon.css';

const shapes = (
  <>
    <circle cx="10.8" cy="7.6" r="6.5" />
    <line x1="6.2" y1="12.2" x2="0.7" y2="17.7" />
  </>
);

export const SearchIcon = () => (
  <svg
    className={s.searchIcon}
    xmlns="http://www.w3.org/2000/svg"
    viewBox="0 0 18.4 18.5"
    aria-hidden="true"
    focusable="false"
  >
    {/* SVG has no text-stroke, so the outline is a wider copy drawn underneath. */}
    <g className={s.outline}>{shapes}</g>
    <g className={s.glyph}>{shapes}</g>
  </svg>
);
