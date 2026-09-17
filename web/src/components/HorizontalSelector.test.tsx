import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render } from '@testing-library/react';
import HorizontalSelector from './HorizontalSelector';

// happy-dom does no layout, so give each item a fixed position keyed by its text.
const X: Record<string, number> = { A: 0, B: 100, C: 200 };

const items = ['A', 'B', 'C'].map((label) => ({
  key: label.toLowerCase(),
  content: <span>{label}</span>,
}));

const indicator = (container: HTMLElement) =>
  container.querySelector<HTMLElement>('[class*="indicator"]');

beforeEach(() => {
  vi.spyOn(HTMLElement.prototype, 'offsetLeft', 'get').mockImplementation(function (
    this: HTMLElement,
  ) {
    return X[this.textContent ?? ''] ?? 0;
  });
  vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockReturnValue(50);
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('HorizontalSelector', () => {
  it('fades the indicator in on the active item on first mount', () => {
    const { container } = render(
      <HorizontalSelector persistKey="fresh" items={items} activeKey="c" />,
    );
    const el = indicator(container);
    expect(el).not.toBeNull();
    expect(el!.style.opacity).toBe('0');
    expect(el!.style.transform).toBe('translateX(200px)');
  });

  // The nav is rendered by each routed page, so navigating replaces the selector
  // with a new instance; its indicator must start from where the old one was.
  it('starts a remounted indicator at the previous instance position', () => {
    const first = render(<HorizontalSelector persistKey="nav" items={items} activeKey="b" />);
    first.unmount();

    const { container } = render(
      <HorizontalSelector persistKey="nav" items={items} activeKey="c" />,
    );
    const el = indicator(container);
    expect(el).not.toBeNull();
    expect(el!.style.opacity).toBe('1');
    expect(el!.style.transform).toBe('translateX(100px)');
  });
});
