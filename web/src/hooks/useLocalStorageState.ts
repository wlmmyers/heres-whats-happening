import { useCallback, useSyncExternalStore } from 'react';

/**
 * Snapshot cache keyed by localStorage key.
 *
 * `useSyncExternalStore` requires `getSnapshot` to return a referentially
 * stable value while the underlying data is unchanged — otherwise React
 * re-renders in an infinite loop. Parsing JSON produces a fresh object every
 * call, so we cache the parsed result against the raw string and only re-parse
 * when the raw string actually changes. Keying by localStorage key also lets
 * multiple hook instances share one identity for the same value.
 */
const snapshotCache = new Map<string, { raw: string | null; parsed: unknown }>();

const getSnapshot = <StateValueType>(localStorageKey: string): StateValueType | null => {
  const raw = localStorage.getItem(localStorageKey);
  const cached = snapshotCache.get(localStorageKey);
  if (cached && cached.raw === raw) {
    return cached.parsed as StateValueType | null;
  }
  let parsed: StateValueType | null = null;
  if (raw !== null) {
    try {
      parsed = JSON.parse(raw) as StateValueType;
    } catch {
      // Corrupt/legacy value: treat as absent rather than crashing render.
      parsed = null;
    }
  }
  snapshotCache.set(localStorageKey, { raw, parsed });
  return parsed;
};

/**
 * Subscribe to changes for a key from both directions:
 * - same tab: a custom event on `document`, dispatched by this hook's actions
 *   (the native `storage` event does NOT fire in the tab that made the change);
 * - other tabs: the native `storage` event on `window`.
 */
const subscribe = (localStorageKey: string, onChange: () => void): (() => void) => {
  const onStorage = (event: StorageEvent) => {
    // event.key is null when storage is cleared wholesale.
    if (event.key === localStorageKey || event.key === null) {
      onChange();
    }
  };
  document.addEventListener(localStorageKey, onChange, false);
  window.addEventListener('storage', onStorage);
  return () => {
    document.removeEventListener(localStorageKey, onChange);
    window.removeEventListener('storage', onStorage);
  };
};

/** A localStorage state hook that will trigger renders when the state changes */
export const useLocalStorageState = <StateValueType = Record<string, string | null | undefined>>(
  key: string,
) => {
  const localStorageKey = key;

  const state = useSyncExternalStore(
    useCallback((onChange: () => void) => subscribe(localStorageKey, onChange), [localStorageKey]),
    useCallback(() => getSnapshot<StateValueType>(localStorageKey), [localStorageKey]),
  );

  const dispatchStateChangeEvent = useCallback(() => {
    document.dispatchEvent(new Event(localStorageKey));
  }, [localStorageKey]);

  return {
    state,
    actions: {
      clear: useCallback(() => {
        localStorage.removeItem(localStorageKey);
        dispatchStateChangeEvent();
      }, [localStorageKey, dispatchStateChangeEvent]),
      clearKey: useCallback(
        <K extends keyof StateValueType>(key: K) => {
          const current = getSnapshot<StateValueType>(localStorageKey);
          if (current) {
            const newState = { ...current };
            delete newState[key];
            localStorage.setItem(localStorageKey, JSON.stringify(newState));
            dispatchStateChangeEvent();
          }
        },
        [localStorageKey, dispatchStateChangeEvent],
      ),
      clearKeys: useCallback(
        <K extends keyof StateValueType>(keys: K[]) => {
          const current = getSnapshot<StateValueType>(localStorageKey);
          if (current) {
            const newState = { ...current };
            keys.forEach((key) => {
              delete newState[key];
            });
            localStorage.setItem(localStorageKey, JSON.stringify(newState));
            dispatchStateChangeEvent();
          }
        },
        [localStorageKey, dispatchStateChangeEvent],
      ),
      setKeyValue: useCallback(
        <K extends keyof StateValueType>(key: K, value: StateValueType[K]) => {
          const newState = {
            ...getSnapshot<StateValueType>(localStorageKey),
            [key]: value,
          };
          localStorage.setItem(localStorageKey, JSON.stringify(newState));
          dispatchStateChangeEvent();
        },
        [localStorageKey, dispatchStateChangeEvent],
      ),
      setValue: useCallback(
        (value: StateValueType) => {
          localStorage.setItem(localStorageKey, JSON.stringify(value));
          dispatchStateChangeEvent();
        },
        [localStorageKey, dispatchStateChangeEvent],
      ),
    },
  };
};
