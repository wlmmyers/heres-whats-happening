import { NavLink, Outlet, useLocation, useSearchParams } from 'react-router-dom';
import clsx from 'clsx';
import { useAuth } from '../auth/useAuth';
import HorizontalSelector from './HorizontalSelector';
import UserMenu from './UserMenu';
import WelcomeModal from './WelcomeModal';
import ConfirmErrorModal from './ConfirmErrorModal';
import * as s from './Layout.css';

const link = ({ isActive }: { isActive: boolean }) =>
  clsx(s.navLink, isActive ? s.navLinkActive : s.navLinkInactive);

// Single source of truth for the nav: rendered in order, and used to decide which
// link the sliding border should hug.
const navItems = [
  { to: '/calendar/seattle', label: 'Calendar' },
  { to: '/interests', label: 'Interests' },
  { to: '/settings', label: 'Settings' },
] as const;

// Our own active check, mirroring react-router's default NavLink matching
// (exact path or a descendant on a segment boundary). Deciding this ourselves
// keeps the indicator independent of how NavLink renders active state.
const isActivePath = (pathname: string, to: string) =>
  pathname === to || pathname.startsWith(to + '/');

export default function Layout() {
  const { status } = useAuth();
  const authed = status === 'authenticated';

  // Which nav item the sliding border should hug, derived from the URL (mirroring
  // NavLink's matching) so it stays independent of NavLink's rendered output.
  const location = useLocation();
  const activeKey = navItems.find((item) => isActivePath(location.pathname, item.to))?.to ?? null;
  const selectorItems = navItems.map((item) => ({
    key: item.to,
    content: (
      <NavLink to={item.to} className={link}>
        {item.label}
      </NavLink>
    ),
  }));

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
        <div className={s.logo} />
        {authed && (
          <>
            <HorizontalSelector
              as="nav"
              aria-label="Primary"
              items={selectorItems}
              activeKey={activeKey}
            />
            <UserMenu />
          </>
        )}
      </header>
      <main className={clsx(s.main, { [s.mainLoggedOut]: !authed })}>
        <Outlet />
        <div className={s.footer}>
          <p>&copy; 2026 Here's What's Happening. All rights reserved.</p>
        </div>
      </main>
      {showWelcome && <WelcomeModal onDismiss={() => dismiss('welcome')} />}
      {showConfirmError && <ConfirmErrorModal onDismiss={() => dismiss('confirmerror')} />}
    </div>
  );
}
