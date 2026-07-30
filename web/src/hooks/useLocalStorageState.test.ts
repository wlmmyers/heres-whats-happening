import { describe, it, expect, beforeEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useLocalStorageState } from './useLocalStorageState';

beforeEach(() => {
  localStorage.clear();
});

describe('useLocalStorageState', () => {
  it('reads the existing value from localStorage on mount', () => {
    localStorage.setItem('k', JSON.stringify({ a: '1' }));
    const { result } = renderHook(() => useLocalStorageState<{ a: string }>('k'));
    expect(result.current.state).toEqual({ a: '1' });
  });

  it('returns null when the key is absent', () => {
    const { result } = renderHook(() => useLocalStorageState('missing'));
    expect(result.current.state).toBeNull();
  });

  it('setValue writes to localStorage and re-renders with the new value', () => {
    const { result } = renderHook(() => useLocalStorageState<{ a: string }>('k'));
    act(() => result.current.actions.setValue({ a: '1' }));
    expect(JSON.parse(localStorage.getItem('k')!)).toEqual({ a: '1' });
    expect(result.current.state).toEqual({ a: '1' });
  });

  it('setKeyValue merges a single key into the stored object', () => {
    localStorage.setItem('k', JSON.stringify({ a: '1' }));
    const { result } = renderHook(() => useLocalStorageState<{ a: string; b: string }>('k'));
    act(() => result.current.actions.setKeyValue('b', '2'));
    expect(result.current.state).toEqual({ a: '1', b: '2' });
  });

  it('clearKey removes a single key from the stored object', () => {
    localStorage.setItem('k', JSON.stringify({ a: '1', b: '2' }));
    const { result } = renderHook(() => useLocalStorageState<{ a: string; b: string }>('k'));
    act(() => result.current.actions.clearKey('a'));
    expect(result.current.state).toEqual({ b: '2' });
  });

  it('clearKeys removes multiple keys from the stored object', () => {
    localStorage.setItem('k', JSON.stringify({ a: '1', b: '2', c: '3' }));
    const { result } = renderHook(() =>
      useLocalStorageState<{ a: string; b: string; c: string }>('k'),
    );
    act(() => result.current.actions.clearKeys(['a', 'c']));
    expect(result.current.state).toEqual({ b: '2' });
  });

  it('clear removes the entire key from localStorage', () => {
    localStorage.setItem('k', JSON.stringify({ a: '1' }));
    const { result } = renderHook(() => useLocalStorageState('k'));
    act(() => result.current.actions.clear());
    expect(localStorage.getItem('k')).toBeNull();
    expect(result.current.state).toBeNull();
  });

  it('propagates a write to another hook instance using the same key', () => {
    const { result } = renderHook(() => ({
      a: useLocalStorageState<{ n: string }>('shared'),
      b: useLocalStorageState<{ n: string }>('shared'),
    }));
    act(() => result.current.a.actions.setValue({ n: '1' }));
    expect(result.current.b.state).toEqual({ n: '1' });
  });

  it('returns null when the stored value is not valid JSON instead of throwing', () => {
    localStorage.setItem('k', '{ not json');
    const { result } = renderHook(() => useLocalStorageState('k'));
    expect(result.current.state).toBeNull();
  });

  it('updates when another tab changes the value (native storage event)', () => {
    const { result } = renderHook(() => useLocalStorageState<{ n: string }>('k'));
    expect(result.current.state).toBeNull();
    act(() => {
      const newValue = JSON.stringify({ n: '9' });
      localStorage.setItem('k', newValue);
      window.dispatchEvent(new StorageEvent('storage', { key: 'k', newValue }));
    });
    expect(result.current.state).toEqual({ n: '9' });
  });
});
