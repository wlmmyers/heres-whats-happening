import { NavLink, Outlet } from 'react-router-dom';
import clsx from 'clsx';
import { useAuth } from '../auth/useAuth';
import UserMenu from './UserMenu';
import * as s from './Layout.css';

const link = ({ isActive }: { isActive: boolean }) =>
  clsx(s.navLink, isActive ? s.navLinkActive : s.navLinkInactive);

export default function Layout() {
  const { status } = useAuth();
  const authed = status === 'authenticated';

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
    </div>
  );
}
