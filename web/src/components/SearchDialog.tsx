import { useEffect, useId, useState } from 'react';
import { createPortal } from 'react-dom';
import { useNavigate } from 'react-router-dom';
import clsx from 'clsx';
import { DIALOG_ROOT_ID } from './Layout';
import { useDebouncedValue } from '../hooks/useDebouncedValue';
import { useEventSearch, SEARCH_MIN_LENGTH } from '../hooks/useEventSearch';
import * as c from '../styles/common.css';
import * as s from './SearchDialog.css';

interface Props {
  open: boolean;
  onClose: () => void;
  cityId?: string;
}

// Collapses a typed word into one request. See useDebouncedValue for why.
const DEBOUNCE_MS = 400;

/**
 * Typeahead search over the city's upcoming events, opened from the Search
 * button in the calendar page header.
 *
 * The body lives in a child that only exists while the dialog is open, so a
 * cancelled search is discarded by unmounting rather than by clearing state on
 * the way in. It renders through a portal into Layout's dialog root for the
 * same reason AddManualEventDialog does: the calendar's animated, transformed
 * ancestors would otherwise clip the backdrop.
 */
export default function SearchDialog({ open, onClose, cityId }: Props) {
  if (!open) return null;
  return <SearchDialogBody onClose={onClose} cityId={cityId} />;
}

function SearchDialogBody({ onClose, cityId }: Omit<Props, 'open'>) {
  const titleId = useId();
  const listId = useId();
  const navigate = useNavigate();
  const [query, setQuery] = useState('');
  const [activeIndex, setActiveIndex] = useState(-1);
  const debounced = useDebouncedValue(query, DEBOUNCE_MS);
  const { data, isFetching } = useEventSearch(cityId, debounced);
  // Resolved once, on mount: re-resolving on later renders would move the
  // portal and remount the body, discarding whatever has been typed into it.
  // The fallback covers rendering the dialog outside a Layout, as the tests do.
  const [host] = useState(() => document.getElementById(DIALOG_ROOT_ID) ?? document.body);

  const results = data?.results ?? [];
  const tooShort = [...debounced.trim()].length < SEARCH_MIN_LENGTH;
  // Single source of truth for whether the listbox is actually in the DOM.
  // aria-expanded/aria-controls/aria-activedescendant and the render branch
  // below all key off this so they can't drift apart and point at an id
  // nothing renders. Checking tooShort here (not just results.length) matters
  // because keepPreviousData lets stale results outlive the query that
  // produced them: shrinking the query back below SEARCH_MIN_LENGTH flips
  // tooShort true while the previous hit is still sitting in `results`.
  const showListbox = !tooShort && results.length > 0;

  // A stale active row would point at a different event once the results it
  // indexes change. Reset it during render rather than in an effect: an
  // effect-based reset would commit one extra render with the old index still
  // pointing into the new list before catching up. This is React's documented
  // pattern for adjusting state when an input changes (setState during
  // render, guarded so it fires only once per change).
  const [resetFor, setResetFor] = useState(debounced);
  if (resetFor !== debounced) {
    setResetFor(debounced);
    setActiveIndex(-1);
  }

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [onClose]);

  const openResult = (index: number) => {
    const hit = results[index];
    if (!hit) return;
    navigate(`/events/${hit.id}`);
    onClose();
  };

  const onFieldKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setActiveIndex((i) => Math.min(i + 1, results.length - 1));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setActiveIndex((i) => Math.max(i - 1, 0));
    } else if (e.key === 'Enter') {
      e.preventDefault();
      openResult(activeIndex >= 0 ? activeIndex : 0);
    }
  };

  return createPortal(
    <div className={c.backdrop} onClick={onClose} data-testid="search-backdrop">
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        className={c.dialog}
        onClick={(e) => e.stopPropagation()}
      >
        <h2 id={titleId}>Search events</h2>
        <input
          autoFocus
          type="text"
          role="combobox"
          aria-expanded={showListbox}
          aria-controls={showListbox ? listId : undefined}
          aria-activedescendant={
            showListbox && activeIndex >= 0 ? `${listId}-${activeIndex}` : undefined
          }
          aria-label="Search events"
          className={s.input}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={onFieldKeyDown}
          placeholder="Artist, event, or venue"
        />

        {tooShort ? (
          <p className={s.status}>Keep typing — at least {SEARCH_MIN_LENGTH} characters.</p>
        ) : isFetching && results.length === 0 ? (
          <p className={s.status}>Searching…</p>
        ) : !showListbox ? (
          <p className={s.status}>No events match “{debounced.trim()}”.</p>
        ) : (
          <ul id={listId} role="listbox" aria-label="Search results" className={s.list}>
            {results.map((hit, i) => (
              <li
                key={hit.id}
                id={`${listId}-${i}`}
                role="option"
                aria-selected={i === activeIndex}
                className={clsx(s.option, i === activeIndex && s.optionActive)}
                onMouseEnter={() => setActiveIndex(i)}
                onClick={() => openResult(i)}
              >
                <div>{hit.title}</div>
                <div className={s.meta}>{hit.venue.name}</div>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>,
    host,
  );
}
