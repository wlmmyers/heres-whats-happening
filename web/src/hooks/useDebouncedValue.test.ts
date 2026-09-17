import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useDebouncedValue } from './useDebouncedValue';

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe('useDebouncedValue', () => {
  it('returns the initial value immediately', () => {
    const { result } = renderHook(() => useDebouncedValue('a', 400));
    expect(result.current).toBe('a');
  });

  it('does not update before the delay elapses', () => {
    const { result, rerender } = renderHook(({ v }) => useDebouncedValue(v, 400), {
      initialProps: { v: 'a' },
    });
    rerender({ v: 'b' });
    act(() => {
      vi.advanceTimersByTime(399);
    });
    expect(result.current).toBe('a');
  });

  it('updates once the delay elapses', () => {
    const { result, rerender } = renderHook(({ v }) => useDebouncedValue(v, 400), {
      initialProps: { v: 'a' },
    });
    rerender({ v: 'b' });
    act(() => {
      vi.advanceTimersByTime(400);
    });
    expect(result.current).toBe('b');
  });

  // The property that keeps a typed word to one request rather than one per key.
  it('collapses a burst of changes into the final value', () => {
    const { result, rerender } = renderHook(({ v }) => useDebouncedValue(v, 400), {
      initialProps: { v: 'm' },
    });
    for (const v of ['mi', 'mid', 'midn', 'midni']) {
      rerender({ v });
      act(() => {
        vi.advanceTimersByTime(100);
      });
    }
    expect(result.current).toBe('m');
    act(() => {
      vi.advanceTimersByTime(400);
    });
    expect(result.current).toBe('midni');
  });
});
