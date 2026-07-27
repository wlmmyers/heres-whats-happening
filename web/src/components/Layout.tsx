import { NavLink, Outlet, useSearchParams } from 'react-router-dom';
import clsx from 'clsx';
import { useAuth } from '../auth/useAuth';
import UserMenu from './UserMenu';
import WelcomeModal from './WelcomeModal';
import ConfirmErrorModal from './ConfirmErrorModal';
import * as s from './Layout.css';

const link = ({ isActive }: { isActive: boolean }) =>
  clsx(s.navLink, isActive ? s.navLinkActive : s.navLinkInactive);

export default function Layout() {
  const { status } = useAuth();
  const authed = status === 'authenticated';

  // The modals live here rather than on a page so they survive the index
  // route's redirect and still render when the SPA is anonymous — the
  // confirm link is often opened on a phone with no session.
  const [params, setParams] = useSearchParams();
  const showWelcome = params.get('welcome') === 'true';
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
            <NavLink to="/calendar/seattle" className={link}>
              Calendar
            </NavLink>
            <NavLink to="/interests" className={link}>
              Interests
            </NavLink>
            <NavLink to="/settings" className={link}>
              Settings
            </NavLink>
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
