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
      <header className={clsx(s.header, { [s.headerLoggedOut]: !authed })}>
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
      </main>
    </div>
  );
}
