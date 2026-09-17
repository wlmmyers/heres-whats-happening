import { NavLink, useLocation, useNavigate, useSearchParams } from 'react-router-dom';
import { useMemo, type ReactNode } from 'react';
import clsx from 'clsx';
import { useAuth } from '../auth/useAuth';
import HorizontalSelector from './HorizontalSelector';
import UserMenu from './UserMenu';
import WelcomeDialog from './WelcomeDialog';
import ConfirmErrorDialog from './ConfirmErrorDialog';
import * as s from './Layout.css';
import { useScreenSize } from '../hooks/useScreenSize';

/**
 * Host node for dialogs that portal out of the page.
 *
 * A `position: fixed` backdrop is positioned against the nearest transformed
 * ancestor rather than the viewport, and a z-index only competes inside its own
 * stacking context — so a dialog rendered in place inside an animated or
 * transformed card is clipped and painted under its neighbours. Portaling into
 * this node puts it back at the top level, above everything the page renders.
 */
export const DIALOG_ROOT_ID = 'dialog-root';

const link = ({ isActive }: { isActive: boolean }) =>
  clsx(s.navLink, isActive ? s.navLinkActive : s.navLinkInactive);

// Our own active check, mirroring react-router's default NavLink matching
// (exact path or a descendant on a segment boundary). Deciding this ourselves
// keeps the indicator independent of how NavLink renders active state.
const isActivePath = (pathname: string, to: string) =>
  pathname === to || pathname.startsWith(to + '/');

/**
 * Renders page background, header, nav, footer and the confirmation modals.
 *
 * Each routed page renders this itself as its outermost element.
 */
export default function Layout({ children, wide }: { children?: ReactNode; wide?: boolean }) {
  const { status } = useAuth();
  const authed = status === 'authenticated';
  const navigate = useNavigate();
  const { isPhoneWidth } = useScreenSize();

  // Which nav item the sliding border should hug, derived from the URL (mirroring
  // NavLink's matching) so it stays independent of NavLink's rendered output.
  const location = useLocation();

  // Single source of truth for the nav: rendered in order, and used to decide which
  // link the sliding border should hug.
  const navItems = useMemo(
    () => [
      {
        to: '/calendar/seattle',
        activeLabel: 'Calendar',
        mobileLabel: 'Cal',
      },
      {
        to: '/interests',
        activeLabel: 'Interests',
        mobileLabel: 'Interests',
      },
      {
        to: '/settings',
        activeLabel: 'Settings',
        mobileLabel: 'Settings',
      },
      ...(isPhoneWidth
        ? [
            {
              to: '/my-shows',
              activeLabel: 'Shows',
              mobileLabel: 'Shows',
            },
          ]
        : []),
    ],
    [isPhoneWidth],
  );

  const activeKey = navItems.find((item) => isActivePath(location.pathname, item.to))?.to ?? null;
  // The modals live here rather than on a page so they survive any redirects
  const [params, setParams] = useSearchParams();
  // If unauthed, user will be redirected to /login and the welcome message
  // will be shown within that modal
  const showWelcome = authed && params.get('welcome') === 'true';
  const showConfirmError = params.get('confirmerror') === 'true';

  const dismiss = (key: string) => {
    const next = new URLSearchParams(params);
    next.delete(key);
    setParams(next, { replace: true });
  };

  return (
    <div className={s.page}>
      <div className={s.background} />
      <header className={clsx(s.header, { [s.hiddenOnPhone]: !authed })}>
        <div className={s.logo} onClick={() => navigate('/')} />
        {authed && (
          <>
            <HorizontalSelector
              className={s.navHorizontalSelector}
              persistKey="primary-nav"
              as="nav"
              aria-label="Primary"
              items={navItems.map((item) => ({
                key: item.to,
                content: (
                  <NavLink to={item.to} className={link}>
                    {isPhoneWidth ? item.mobileLabel : item.activeLabel}
                  </NavLink>
                ),
              }))}
              activeKey={activeKey}
            />
            <UserMenu />
          </>
        )}
      </header>
      <main className={clsx(s.main, { [s.mainLoggedOut]: !authed, [s.mainWide]: wide })}>
        {children}
        <div className={s.footer}>
          <p>&copy; 2026 Here's What's Happening. All rights reserved.</p>
        </div>
      </main>
      <WelcomeDialog open={showWelcome} onClose={() => dismiss('welcome')} />
      <ConfirmErrorDialog open={showConfirmError} onClose={() => dismiss('confirmerror')} />
      <div id={DIALOG_ROOT_ID} />
    </div>
  );
}
