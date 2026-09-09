import { useId, useState, type ReactNode } from 'react';
import { AnimatePresence, motion } from 'motion/react';
import * as s from './CollapsableSection.css';
import * as c from '../styles/common.css';
import RotatingCaret from './RotatingCaret';

/**
 * A card section whose body collapses to just its title row when the header
 * row is activated. Starts expanded.
 */
export default function CollapsableSection({
  title,
  children,
  defaultOpen = true,
}: {
  title: string;
  children: ReactNode;
  defaultOpen?: boolean;
}) {
  const [open, setOpen] = useState<boolean>(defaultOpen);
  const bodyId = useId();

  return (
    <section className={s.collapsableSection}>
      {/* The heading wraps the toggle rather than sitting inside it: the whole
          header row is the control, but a heading is not phrasing content and
          so cannot live in a <button>. `aria-expanded` carries the open state,
          which is why the button is labelled by its title alone. */}
      <h2 className={c.sectionTitle}>
        <button
          type="button"
          className={s.header}
          aria-expanded={open}
          aria-controls={bodyId}
          onClick={() => setOpen((wasOpen) => !wasOpen)}
        >
          {title}
          <RotatingCaret open={open} className={s.caret} />
        </button>
      </h2>
      {/* The id stays mounted so `aria-controls` always resolves, collapsed or not. */}
      <div id={bodyId} className={s.body}>
        <AnimatePresence initial={false}>
          {open && (
            <motion.div
              key="body"
              initial={{ height: 0, opacity: 0 }}
              animate={{ height: 'auto', opacity: 1 }}
              exit={{ height: 0, opacity: 0 }}
              transition={{ type: 'spring', stiffness: 500, damping: 40 }}
            >
              <div className={s.bodyContent}>{children}</div>
            </motion.div>
          )}
        </AnimatePresence>
      </div>
    </section>
  );
}
