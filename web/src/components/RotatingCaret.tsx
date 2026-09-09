import clsx from 'clsx';
import { motion } from 'motion/react';
import * as s from './RotatingCaret.css';

/**
 * The disclosure chevron for a collapsable section: points down while closed,
 * spins up while `open`. Size and colour live here; `className` is for the
 * caller's placement within its own header row.
 */
export default function RotatingCaret({ open, className }: { open: boolean; className?: string }) {
  return (
    <motion.svg
      className={clsx(s.caret, className)}
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
      // Without this the caret spins in from 0deg on every mount.
      initial={false}
      animate={{ rotate: open ? 180 : 0 }}
      transition={{ type: 'spring', stiffness: 500, damping: 40 }}
    >
      <path d="M3 6l5 5 5-5" />
    </motion.svg>
  );
}
