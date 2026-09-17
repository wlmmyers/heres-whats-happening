import { useEffect, useState } from 'react';

/**
 * Delays propagating `value` until it has been stable for `delayMs`. Used to
 * collapse a burst of keystrokes into one request: the search endpoint shares
 * the 120/min authed rate-limit budget with calendar paging, so one request
 * per keystroke would starve it.
 */
export function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(t);
  }, [value, delayMs]);
  return debounced;
}
