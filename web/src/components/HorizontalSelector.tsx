import { AnimatePresence, motion } from 'motion/react';
import { useLayoutEffect, useRef, useState, type ElementType, type ReactNode } from 'react';
import clsx from 'clsx';
import * as s from './HorizontalSelector.css';

export type HorizontalSelectorItem = {
  /** Stable identifier for the item; compared against `activeKey`. */
  key: string;
  content: ReactNode;
};

export type HorizontalSelectorProps = {
  items: HorizontalSelectorItem[];
  /** Key of the active item, or null when none is active (the border hides). */
  activeKey: string | null;
  /**
   * Called with an item's key when it is clicked. Optional: items that handle
   * their own clicks (e.g. a NavLink) can omit this, in which case the items are
   * not wrapped in a button.
   */
  onSelect?: (key: string) => void;
  /**
   * How the active item is indicated. 'outline' (default) slides a border around
   * it; 'fill' slides a solid blue box behind it and forces the active item's text
   * white for legibility (its content should inherit colour rather than set it).
   */
  itemStyle?: 'outline' | 'fill';
  /**
   * Carries the indicator's position across remounts: a new selector with the same
   * key starts its indicator where the previous one left it and slides to its own
   * active item. Needed when the selector is re-created on navigation (e.g. the nav,
   * which each routed page renders inside its own Layout). Unique per selector.
   */
  persistKey?: string;
  /** Container element type. Defaults to 'div'; pass 'nav' for a landmark. */
  as?: ElementType;
  className?: string;
  'aria-label'?: string;
};

type Rect = { x: number; width: number };

// Where each persistKey'd selector's indicator last sat, for its next instance to
// start from.
const lastRects = new Map<string, Rect>();

/**
 * A horizontal row of items with an indicator that slides to hug the active one
 * (an outline or a solid fill, per `itemStyle`). Active selection is controlled by
 * the caller via `activeKey`, so the component stays independent of how "active" is
 * determined (routing, local state, ...).
 */
export default function HorizontalSelector({
  items,
  activeKey,
  onSelect,
  itemStyle = 'outline',
  persistKey,
  as: Root = 'div',
  className,
  'aria-label': ariaLabel,
}: HorizontalSelectorProps) {
  const itemRefs = useRef<(HTMLElement | null)[]>([]);
  // A remount starts from the previous instance's indicator position (if any), so
  // the measurement below slides it from there rather than it appearing fresh.
  const [rect, setRect] = useState<Rect | null>(() =>
    persistKey ? (lastRects.get(persistKey) ?? null) : null,
  );

  const activeIndex = items.findIndex((item) => item.key === activeKey);

  // Measure the active item (by ref) so motion can slide the indicator to it, and
  // re-measure on resize since item positions/widths shift with the viewport.
  useLayoutEffect(() => {
    const measure = () => {
      const active = activeIndex >= 0 ? itemRefs.current[activeIndex] : null;
      const next = active ? { x: active.offsetLeft, width: active.offsetWidth } : null;
      setRect(next);
      if (persistKey) {
        if (next) lastRects.set(persistKey, next);
        else lastRects.delete(persistKey);
      }
    };
    measure();
    window.addEventListener('resize', measure);
    return () => window.removeEventListener('resize', measure);
  }, [activeIndex, persistKey]);

  return (
    <Root className={clsx(s.container, className)} aria-label={ariaLabel}>
      {/* initial={false}: an indicator restored from a previous instance is present on
          the first render, so it starts fully visible where it was and slides. One
          that appears later (no prior position) still fades in via its `initial`. */}
      <AnimatePresence initial={false}>
        {rect && (
          <motion.div
            key="active-indicator"
            className={clsx(
              s.indicator,
              itemStyle === 'fill' ? s.indicatorFill : s.indicatorOutline,
            )}
            initial={{ opacity: 0, x: rect.x, width: rect.width }}
            animate={{ opacity: 1, x: rect.x, width: rect.width }}
            exit={{ opacity: 0 }}
            transition={{ type: 'spring', stiffness: 500, damping: 40 }}
          />
        )}
      </AnimatePresence>
      {items.map((item, i) => {
        const setRef = (el: HTMLElement | null) => {
          itemRefs.current[i] = el;
        };
        // In fill mode the active item's text is forced white so it reads over the fill.
        const wrapperClass = clsx(
          s.item,
          itemStyle === 'fill' && item.key === activeKey && s.itemFillActive,
        );
        return onSelect ? (
          <button
            key={item.key}
            type="button"
            ref={setRef}
            className={wrapperClass}
            aria-pressed={item.key === activeKey}
            onClick={() => onSelect(item.key)}
          >
            {item.content}
          </button>
        ) : (
          <div key={item.key} ref={setRef} className={wrapperClass}>
            {item.content}
          </div>
        );
      })}
    </Root>
  );
}
