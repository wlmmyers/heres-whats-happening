import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';

vi.mock('../hooks/useScreenSize', () => ({ useScreenSize: vi.fn() }));

import { useScreenSize } from '../hooks/useScreenSize';
import HorizontalSelector from './HorizontalSelector';

function mockPhone(isPhoneWidth: boolean) {
  vi.mocked(useScreenSize).mockReturnValue({
    screenSize: isPhoneWidth ? 'phone' : 'desktop',
    isPhoneWidth,
  } as ReturnType<typeof useScreenSize>);
}

// happy-dom does no layout, so give each item a fixed position keyed by its text.
const X: Record<string, number> = { A: 0, B: 100, C: 200 };

const items = ['A', 'B', 'C'].map((label) => ({
  key: label.toLowerCase(),
  content: <span>{label}</span>,
}));

const indicator = (container: HTMLElement) =>
  container.querySelector<HTMLElement>('[class*="indicator"]');

beforeEach(() => {
  mockPhone(false);
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

  describe('show more', () => {
    const many = Array.from({ length: 10 }, (_, i) => ({
      key: `k${i}`,
      content: <span>{`Item ${i}`}</span>,
    }));

    it('collapses to 8 items and toggles with wrap on a phone', () => {
      mockPhone(true);
      render(<HorizontalSelector wrap items={many} activeKey={null} />);
      expect(screen.queryByText('Item 7')).not.toBeNull();
      expect(screen.queryByText('Item 8')).toBeNull();

      fireEvent.click(screen.getByRole('button', { name: 'Show more' }));
      expect(screen.queryByText('Item 9')).not.toBeNull();

      fireEvent.click(screen.getByRole('button', { name: 'Show fewer' }));
      expect(screen.queryByText('Item 8')).toBeNull();
    });

    it('keeps an active item beyond the cut-off visible when collapsed', () => {
      mockPhone(true);
      const { container, rerender } = render(
        <HorizontalSelector wrap items={many} activeKey="k9" />,
      );
      expect(screen.queryByText('Item 9')).not.toBeNull();
      expect(screen.queryByText('Item 8')).toBeNull();
      expect(indicator(container)).not.toBeNull();

      rerender(<HorizontalSelector wrap items={many} activeKey="k8" />);
      expect(screen.queryByText('Item 8')).not.toBeNull();
      expect(screen.queryByText('Item 9')).toBeNull();
    });

    it.each([
      ['not a phone', { wrap: true, phone: false, count: 10 }],
      ['wrap is off', { wrap: false, phone: true, count: 10 }],
      ['8 or fewer items', { wrap: true, phone: true, count: 8 }],
    ])('shows every item with no toggle when %s', (_, { wrap, phone, count }) => {
      mockPhone(phone);
      render(<HorizontalSelector wrap={wrap} items={many.slice(0, count)} activeKey={null} />);
      expect(screen.queryByText(`Item ${count - 1}`)).not.toBeNull();
      expect(screen.queryByRole('button', { name: /Show/ })).toBeNull();
    });
  });
});
