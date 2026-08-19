import type { ReactNode } from 'react';
import * as s from './ExternalLink.css';
import * as c from '../styles/common.css';
import { clsx } from 'clsx';

/**
 * A link that opens in a new window, tagged with an overlapping-windows icon so
 * the jump off-site is visible before the click. Renders `children` bare when
 * `href` is missing, so callers holding an optional URL don't have to branch.
 */
export default function ExternalLink({
  href,
  children,
  className = c.link,
}: {
  href?: string;
  children: ReactNode;
  className?: string;
}) {
  if (!href) return <>{children}</>;

  return (
    <a href={href} target="_blank" rel="noreferrer" className={clsx(s.externalLink, className)}>
      {children}
      <span className={s.srOnly}> (opens in new window)</span>
      {/* No whitespace text node before the icon, so it can never wrap away
          from the last word of the link. */}
      <svg
        className={s.icon}
        viewBox="0 0 16 16"
        fill="none"
        stroke="currentColor"
        strokeWidth={1.5}
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden
      >
        {/* Front window, lower left. */}
        <rect x="2" y="6" width="8" height="8" />
        {/* Back window, upper right: an open path that stops where the front
            window begins rather than crossing it, so the front reads as on top. */}
        <path d="M6 6V2h8v8h-4" />
      </svg>
    </a>
  );
}
