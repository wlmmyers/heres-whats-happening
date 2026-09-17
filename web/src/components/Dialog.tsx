import { useEffect, useId, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { AnimatePresence, motion, useIsPresent } from 'motion/react';
import clsx from 'clsx';
import { DIALOG_ROOT_ID } from './Layout';
import * as c from '../styles/common.css';
import * as s from './Dialog.css';

interface Props {
  open: boolean;
  heading: ReactNode;
  onClose: () => void;
  children: ReactNode;
  /** Merged onto the dialog card, e.g. to widen it past the default. */
  className?: string;
  /** Whether to position the dialog at the top of the screen on mobile. */
  topOnPhone?: boolean;
  /** Id of an element inside the children that describes the dialog. */
  'aria-describedby'?: string;
}

const transition = { type: 'spring', stiffness: 500, damping: 40 } as const;

/**
 * A modal card with a heading and a close button. It closes on Escape, a
 * backdrop click, or the close button, and animates in and out.
 *
 * Keep it mounted and drive it with `open` rather than rendering it
 * conditionally: unmounting it skips the exit animation. Children are only
 * mounted while the dialog is open or animating out, so state held inside them
 * is discarded on close.
 */
export default function Dialog({ open, ...panelProps }: Props) {
  return <AnimatePresence>{open && <DialogPanel key="dialog" {...panelProps} />}</AnimatePresence>;
}

/**
 * While it animates out, AnimatePresence keeps rendering this with the props
 * from its last open render, so the copy doesn't blank mid-fade even if the
 * caller no longer has anything to show.
 *
 * It renders through a portal into Layout's dialog root: the calendar's
 * animated, transformed ancestors would otherwise clip the backdrop and trap
 * the dialog in their stacking context.
 */
function DialogPanel({
  heading,
  onClose,
  children,
  className,
  'aria-describedby': describedBy,
  topOnPhone,
}: Omit<Props, 'open'>) {
  const headingId = useId();
  const isPresent = useIsPresent();
  // Resolved on mount, which is each time the dialog opens: a dialog whose page
  // mounts it closed renders before Layout has committed the root, so the root
  // isn't there to find any earlier. Re-resolving on later renders instead would
  // move the portal and remount the children, discarding any state they hold.
  // The fallback covers rendering the dialog outside a Layout, as the tests do.
  const [host] = useState(() => document.getElementById(DIALOG_ROOT_ID) ?? document.body);

  useEffect(() => {
    if (!isPresent) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [onClose, isPresent]);

  return createPortal(
    <motion.div
      className={clsx(s.backdrop, { [s.topOnPhone]: topOnPhone })}
      onClick={onClose}
      data-testid="dialog-backdrop"
      // Once closed, the fading dialog is only a picture of itself: inert lets
      // clicks fall through to the page and blurs the focused field, so a second
      // click on Confirm or an Enter in the search box can't fire again.
      inert={!isPresent}
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      transition={transition}
    >
      <motion.div
        role="dialog"
        aria-modal="true"
        aria-labelledby={headingId}
        aria-describedby={describedBy}
        className={clsx(s.dialog, s.closable, className)}
        onClick={(e) => e.stopPropagation()}
        initial={{ opacity: 0, y: 20 }}
        animate={{ opacity: 1, y: 0 }}
        exit={{ opacity: 0, y: 20 }}
        transition={transition}
      >
        <h2 className={clsx(c.sectionTitleLarge, s.heading)} id={headingId}>
          {heading}
        </h2>
        <button type="button" aria-label="Close" className={s.closeButton} onClick={onClose}>
          ×
        </button>
        {children}
      </motion.div>
    </motion.div>,
    host,
  );
}
