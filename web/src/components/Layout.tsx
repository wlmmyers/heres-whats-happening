import { NavLink, Outlet } from 'react-router-dom';
import clsx from 'clsx';
import UserMenu from './UserMenu';
import * as s from './Layout.css';

const link = ({ isActive }: { isActive: boolean }) =>
  clsx(s.navLink, isActive ? s.navLinkActive : s.navLinkInactive);

export default function Layout() {
  return (
    <div className={s.page}>
      <header className={s.header}>
        <div
          style={{ backgroundImage: `url('/titleLogo.png')`, width: '280px', height: '40px' }}
          className={s.logo}
        />
        <NavLink to="/calendar" className={link}>
          Calendar
        </NavLink>
        <NavLink to="/onboarding" className={link}>
          Interests
        </NavLink>
        <NavLink to="/settings" className={link}>
          Settings
        </NavLink>
        <UserMenu />
      </header>
      <main className={s.main}>
        <Outlet />
      </main>
    </div>
  );
}
