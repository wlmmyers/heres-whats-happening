import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { LazyList } from './LazyList';

type IOEntry = { isIntersecting: boolean };

// happy-dom has no IntersectionObserver, so stand in a controllable one that
// records what it observes and lets tests fire the intersection callback.
class MockIntersectionObserver {
  static instances: MockIntersectionObserver[] = [];
  callback: (entries: IOEntry[]) => void;
  options?: IntersectionObserverInit;
  observed: Element[] = [];
  disconnected = false;

  constructor(callback: (entries: IOEntry[]) => void, options?: IntersectionObserverInit) {
    this.callback = callback;
    this.options = options;
    MockIntersectionObserver.instances.push(this);
  }
  observe(el: Element) {
    this.observed.push(el);
  }
  unobserve() {}
  disconnect() {
    this.disconnected = true;
  }
  trigger(isIntersecting: boolean) {
    this.callback([{ isIntersecting }]);
  }
}

beforeEach(() => {
  MockIntersectionObserver.instances = [];
  vi.stubGlobal('IntersectionObserver', MockIntersectionObserver);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('LazyList', () => {
  it('renders its children', () => {
    render(
      <LazyList fetchNextPage={() => {}} hasNextPage>
        <div>child content</div>
      </LazyList>,
    );
    expect(screen.getByText('child content')).toBeInTheDocument();
  });

  it('calls fetchNextPage when the sentinel scrolls into view', () => {
    const fetchNextPage = vi.fn();
    render(
      <LazyList fetchNextPage={fetchNextPage} hasNextPage>
        <div>child content</div>
      </LazyList>,
    );
    MockIntersectionObserver.instances[0].trigger(true);
    expect(fetchNextPage).toHaveBeenCalledTimes(1);
  });

  it('does not call fetchNextPage while the sentinel stays out of view', () => {
    const fetchNextPage = vi.fn();
    render(
      <LazyList fetchNextPage={fetchNextPage} hasNextPage>
        <div>child content</div>
      </LazyList>,
    );
    MockIntersectionObserver.instances[0].trigger(false);
    expect(fetchNextPage).not.toHaveBeenCalled();
  });

  it('does not observe when there is no next page', () => {
    const fetchNextPage = vi.fn();
    render(
      <LazyList fetchNextPage={fetchNextPage} hasNextPage={false}>
        <div>child content</div>
      </LazyList>,
    );
    expect(MockIntersectionObserver.instances).toHaveLength(0);
    expect(fetchNextPage).not.toHaveBeenCalled();
  });
});
